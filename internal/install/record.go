package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// record reads Record, the files install wrote, each a clean, absolute path on a line of its own.
func (s System) record() ([]string, error) {
	data, _, err := s.Files.Read(Record)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", Record, err)
	}
	var paths []string
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !path.IsAbs(line) || path.Clean(line) != line {
			return nil, fmt.Errorf("%s: line %d: %q is not a clean, absolute path", Record, i+1, line)
		}
		paths = append(paths, line)
	}
	return paths, nil
}

// writeRecord writes Record as listing paths, where it lists old.
func (s System) writeRecord(old, paths []string) error {
	if slices.Equal(old, paths) {
		return nil
	}
	var data bytes.Buffer
	data.WriteString("# The files ikeward install wrote, which ikeward uninstall removes.\n")
	for _, p := range paths {
		data.WriteString(p + "\n")
	}
	if err := s.Files.Write(Record, data.Bytes(), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", Record, err)
	}
	return nil
}

// restarts are the daemons install restarts, in daemons' order: those Pending lists, which an
// earlier install left, and those whose settings this one changes. recorded is whether Pending
// exists.
type restarts struct {
	units    []string
	recorded bool
}

// pending reads Pending, a unit of daemons on each line; where there is none, it lists none.
func (s System) pending() (*restarts, error) {
	data, _, err := s.Files.Read(Pending)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &restarts{}, nil
	case err != nil:
		return nil, fmt.Errorf("cannot read %s: %w", Pending, err)
	}
	var listed []string
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !slices.ContainsFunc(daemons, func(d daemon) bool { return d.unit == line }) {
			return nil, fmt.Errorf("%s: line %d: %q is not a daemon install restarts", Pending,
				i+1, line)
		}
		listed = append(listed, line)
	}
	r := &restarts{recorded: true}
	for _, d := range daemons {
		if slices.Contains(listed, d.unit) {
			r.units = append(r.units, d.unit)
		}
	}
	return r, nil
}

// pend adds to r the daemons whose settings changed holds, and has Pending list them before
// install changes those settings. A stop between the two then costs a restart more at worst. A dry
// run writes nothing.
func (s System) pend(r *restarts, changed []string) error {
	units := make([]string, 0, len(daemons))
	for _, d := range daemons {
		if slices.Contains(r.units, d.unit) || changedIn(changed, d.dir) {
			units = append(units, d.unit)
		}
	}
	if len(units) == len(r.units) {
		return nil
	}
	r.units = units
	if s.DryRun {
		return nil
	}
	var data bytes.Buffer
	data.WriteString("# The daemons whose settings ikeward install changes, which a rerun " +
		"restarts where it stops half way.\n")
	for _, unit := range units {
		data.WriteString(unit + "\n")
	}
	if err := s.Files.Write(Pending, data.Bytes(), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", Pending, err)
	}
	r.recorded = true
	return nil
}

// restarted removes Pending, where it exists, once the daemons it lists have restarted and the
// rest of takeUp has run.
func (s System) restarted(r *restarts) error {
	if !r.recorded || s.DryRun {
		return nil
	}
	if err := s.Files.Remove(Pending); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove %s: %w", Pending, err)
	}
	return nil
}
