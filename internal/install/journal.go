package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// owed are the commands of the steps an earlier install owes, each as a line, from Journal and
// Pending. journal is what Journal lists, and inJournal and inPending whether each file exists.
type owed struct {
	lines, journal       []string
	inJournal, inPending bool
}

// has is whether o owes command.
func (o owed) has(command []string) bool {
	return slices.Contains(o.lines, strings.Join(command, " "))
}

// owed reads Journal, and Pending, which v0.2.0 wrote. v0.2.0 took every step on each run, and
// restarted the daemons Pending lists: Pending owes each of those.
func (s System) owed() (owed, error) {
	var o owed
	var err error
	o.journal, o.inJournal, err = s.list(Journal, "a command of install's steps", isStep)
	if err != nil {
		return owed{}, err
	}
	units, inPending, err := s.list(Pending, "a daemon install restarts", func(line string) bool {
		return line == networkd || line == StrongSwan
	})
	if err != nil {
		return owed{}, err
	}
	o.lines, o.inPending = slices.Clone(o.journal), inPending
	for _, st := range steps {
		command := st.command
		if inPending && st.restart != nil && slices.Contains(units, st.restart[len(st.restart)-1]) {
			command = st.restart
		}
		if line := strings.Join(command, " "); inPending && !slices.Contains(o.lines, line) {
			o.lines = append(o.lines, line)
		}
	}
	return o, nil
}

// isStep is whether line is the command of a step of steps.
func isStep(line string) bool {
	return slices.ContainsFunc(steps, func(st step) bool {
		return line == strings.Join(st.command, " ") ||
			st.restart != nil && line == strings.Join(st.restart, " ")
	})
}

// writeJournal has Journal list the commands of the steps p holds due, where any is and Journal
// lists other ones. A stop before they all run then costs a step more at worst. A dry run writes
// nothing.
func (s System) writeJournal(o owed, p plan) error {
	lines := p.lines()
	if s.DryRun || len(lines) == 0 || o.inJournal && slices.Equal(o.journal, lines) {
		return nil
	}
	var data bytes.Buffer
	data.WriteString("# The steps ikeward install owes, which a rerun takes where it stops half " +
		"way.\n")
	for _, line := range lines {
		data.WriteString(line + "\n")
	}
	if err := s.Files.Write(Journal, data.Bytes(), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", Journal, err)
	}
	return nil
}

// ended removes Journal, where o read it or p held a step due, and Pending, where o read it, once
// every step due has run. A dry run removes nothing.
func (s System) ended(o owed, p plan) error {
	if s.DryRun {
		return nil
	}
	for _, name := range []string{Journal, Pending} {
		if name == Journal && !p.due() && !o.inJournal || name == Pending && !o.inPending {
			continue
		}
		if err := s.Files.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("cannot remove %s: %w", name, err)
		}
	}
	return nil
}
