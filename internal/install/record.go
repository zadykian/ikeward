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
	paths, exists, err := s.list(Record, "a clean, absolute path", func(line string) bool {
		return path.IsAbs(line) && path.Clean(line) == line
	})
	if err == nil && !exists {
		err = fmt.Errorf("cannot read %s: %w", Record, fs.ErrNotExist)
	}
	return paths, err
}

// list reads the lines of the file at name but blank ones and comments, which start with #. Each
// must be what valid takes, which what names. exists is whether there is the file.
func (s System) list(name, what string, valid func(string) bool) ([]string, bool, error) {
	data, _, err := s.Files.Read(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("cannot read %s: %w", name, err)
	}
	var lines []string
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !valid(line) {
			return nil, true, fmt.Errorf("%s: line %d: %q is not %s", name, i+1, line, what)
		}
		lines = append(lines, line)
	}
	return lines, true, nil
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

// restarts are the daemons install restarts, in steps' order: those Pending lists, which an
// earlier install left, and those whose settings this one changes. recorded is whether Pending
// exists.
type restarts struct {
	units    []string
	recorded bool
}

// pending reads Pending, a daemon's unit on each line; where there is none, it lists none.
func (s System) pending() (*restarts, error) {
	listed, recorded, err := s.list(Pending, "a daemon install restarts", func(line string) bool {
		return line == networkd || line == StrongSwan
	})
	if err != nil {
		return nil, err
	}
	r := &restarts{recorded: recorded}
	for _, st := range steps {
		if unit := st.restart; unit != nil && slices.Contains(listed, unit[len(unit)-1]) {
			r.units = append(r.units, unit[len(unit)-1])
		}
	}
	return r, nil
}

// pend adds to r the daemons whose settings changed holds, and has Pending list them before
// install changes those settings. A stop between the two then costs a restart more at worst. A dry
// run writes nothing.
func (s System) pend(r *restarts, changed []string) error {
	var units []string
	for _, st := range steps {
		if st.restart == nil {
			continue
		}
		unit := st.restart[len(st.restart)-1]
		if _, ok := changedIn(changed, st.own); ok || slices.Contains(r.units, unit) {
			units = append(units, unit)
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
// rest of the steps have run.
func (s System) restarted(r *restarts) error {
	if !r.recorded || s.DryRun {
		return nil
	}
	if err := s.Files.Remove(Pending); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove %s: %w", Pending, err)
	}
	return nil
}
