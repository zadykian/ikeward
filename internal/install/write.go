package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/zadykian/ikeward/internal/files"
)

// change is a file of install's that the system lacks, or has otherwise.
type change struct {
	files.File
	// exists is whether the system has the file, and old and oldMode what it holds and its mode.
	exists  bool
	old     []byte
	oldMode fs.FileMode
}

// secret is whether only root may read the file, as install writes it or as the system has it:
// install then never prints its content.
func (c change) secret() bool {
	return c.Mode&0o077 == 0 || (c.exists && c.oldMode&0o077 == 0)
}

// update is what install changes of its files: those of want that the system lacks or has
// otherwise, and how many it has as they are. Then those Record lists but install no longer
// writes. Where Record exists, record holds what it lists.
type update struct {
	want    []files.File
	changes []change
	same    int
	stale   []string
	record  []string
}

// update reads what install changes to write want.
func (s System) update(want []files.File) (update, error) {
	u := update{want: want}
	var err error
	u.record, err = s.record()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return update{}, err
	}
	if u.changes, u.same, err = s.changes(want); err != nil {
		return update{}, err
	}
	paths := u.paths()
	for _, p := range u.record {
		if !slices.Contains(paths, p) {
			u.stale = append(u.stale, p)
		}
	}
	return u, nil
}

// paths are the paths of the files install writes.
func (u update) paths() []string {
	paths := make([]string, 0, len(u.want))
	for _, f := range u.want {
		paths = append(paths, f.Path)
	}
	return paths
}

// changed are the paths of the files install writes or removes.
func (u update) changed() []string {
	changed := slices.Clone(u.stale)
	for _, c := range u.changes {
		changed = append(changed, c.Path)
	}
	return changed
}

// write writes the files of u that differ on the system and removes the stale ones, naming each.
// It keeps Record listing the files install wrote. A dry run prints the changes as diffs, but for
// a file only root may read, and writes nothing.
func (s System) write(out *printer, u update) error {
	paths := u.paths()
	// Record gains the new files before they are written, so that uninstall finds them even where
	// install stops half way, and loses the old ones once they are gone.
	both := append(slices.Clone(paths), u.stale...)
	if !s.DryRun {
		if err := s.writeRecord(u.record, both); err != nil {
			return err
		}
	}
	for _, c := range u.changes {
		if err := s.writeChange(out, c); err != nil {
			return err
		}
	}
	if u.same > 0 {
		out.printf("Left %d of the %d files as they were\n", u.same, len(u.want))
	}
	for _, p := range u.stale {
		if err := s.remove(out, p); err != nil {
			return err
		}
	}
	if s.DryRun {
		return nil
	}
	return s.writeRecord(both, paths)
}

// writeChange writes c, or in a dry run prints it.
func (s System) writeChange(out *printer, c change) error {
	if s.DryRun {
		out.printf("%s", c.preview())
		return nil
	}
	if err := s.Files.Write(c.Path, c.Data, c.Mode); err != nil {
		return fmt.Errorf("cannot write %s: %w", c.Path, err)
	}
	verb := "Created"
	if c.exists {
		verb = "Updated"
	}
	out.printf("%s %s\n", verb, c.Path)
	return nil
}

// changes returns the files of want that the system lacks or has otherwise, in their order, and
// how many it has as they are.
func (s System) changes(want []files.File) ([]change, int, error) {
	var changes []change
	same := 0
	for _, f := range want {
		data, mode, err := s.Files.Read(f.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			changes = append(changes, change{File: f})
		case err != nil:
			return nil, 0, fmt.Errorf("cannot read %s: %w", f.Path, err)
		case bytes.Equal(data, f.Data) && mode == f.Mode:
			same++
		default:
			changes = append(changes, change{File: f, exists: true, old: data, oldMode: mode})
		}
	}
	return changes, same, nil
}

// preview is what a dry run prints of c: what install would do, and a unified diff of the content
// unless only root may read it.
func (c change) preview() string {
	if !c.exists {
		if c.secret() {
			return fmt.Sprintf("Would create %s, mode %04o, which only root may read: not shown\n",
				c.Path, c.Mode)
		}
		return fmt.Sprintf("Would create %s, mode %04o:\n%s", c.Path, c.Mode,
			unified("/dev/null", c.Path, nil, c.Data))
	}
	head := "Would update " + c.Path
	if c.oldMode != c.Mode {
		head += fmt.Sprintf(", mode %04o to %04o", c.oldMode, c.Mode)
	}
	switch {
	case bytes.Equal(c.old, c.Data):
		return head + "\n"
	case c.secret():
		return head + ", which only root may read: not shown\n"
	}
	return head + ":\n" + unified(c.Path, c.Path, c.old, c.Data)
}
