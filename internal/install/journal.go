package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// owed are the commands of the steps an earlier install owes, each as a line: the restarts of the
// daemons Pending lists. inPending is whether Pending exists.
type owed struct {
	lines     []string
	inPending bool
}

// has is whether o owes command.
func (o owed) has(command []string) bool {
	return slices.Contains(o.lines, strings.Join(command, " "))
}

// owed reads Pending, the daemons whose restart an earlier install owes.
func (s System) owed() (owed, error) {
	units, inPending, err := s.list(Pending, "a daemon install restarts", func(line string) bool {
		return line == networkd || line == StrongSwan
	})
	if err != nil {
		return owed{}, err
	}
	o := owed{inPending: inPending}
	for _, st := range steps {
		if st.restart != nil && slices.Contains(units, st.restart[len(st.restart)-1]) {
			o.lines = append(o.lines, strings.Join(st.restart, " "))
		}
	}
	return o, nil
}

// pend has Pending list the daemons whose restart p holds due, where o owes fewer, before install
// changes their settings. A stop between the two then costs a restart more at worst. A dry run
// writes nothing.
func (s System) pend(o owed, p plan) error {
	var units []string
	for i, d := range p {
		if d != nil && d.restart {
			units = append(units, steps[i].restart[len(steps[i].restart)-1])
		}
	}
	if s.DryRun || len(units) == len(o.lines) {
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
	return nil
}

// ended removes Pending, where o read it or p held a restart due, once every step has run. A dry
// run removes nothing.
func (s System) ended(o owed, p plan) error {
	restarts := slices.ContainsFunc(p, func(d *due) bool { return d != nil && d.restart })
	if s.DryRun || !o.inPending && !restarts {
		return nil
	}
	if err := s.Files.Remove(Pending); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove %s: %w", Pending, err)
	}
	return nil
}
