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
