package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// tree adds the findings of the files git lists in dir, new ones included, or those of them that
// args name: their paths, and what they hold on disk. A symbolic link holds its target's path.
func tree(dir string, args []string, terms []term, r *report) error {
	out, err := gitOutput(dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	listed := strings.FieldsFunc(out, func(c rune) bool { return c == 0 })
	slices.Sort(listed)
	inScope, err := scope(dir, args)
	if err != nil {
		return err
	}
	for _, path := range slices.Compact(listed) {
		if !inScope(path) {
			continue
		}
		name := r.path(path, terms)
		data, err := read(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			return masked(err, terms)
		}
		r.text(name, data, terms)
	}
	return nil
}

// read returns what the listed file at name holds, or a symbolic link's target. It returns
// nothing for a file gone from disk, or one of another kind, as a submodule or a nested
// repository, which git lists as DIR/.
func read(name string) ([]byte, error) {
	info, err := os.Lstat(strings.TrimSuffix(name, "/"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(name)
		return []byte(target), err
	case !info.Mode().IsRegular():
		return nil, nil
	}
	return os.ReadFile(name)
}

// scope returns whether a path from the repository's root is among the FILE arguments, all paths
// where there are none. An argument outside dir names nothing.
func scope(dir string, args []string) (func(string) bool, error) {
	if len(args) == 0 {
		return func(string) bool { return true }, nil
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]bool)
	for _, arg := range args {
		path := arg
		if filepath.IsAbs(arg) {
			if path, err = filepath.Rel(root, arg); err != nil {
				continue
			}
		}
		path = filepath.ToSlash(filepath.Clean(path))
		if path != ".." && !strings.HasPrefix(path, "../") {
			paths[path] = true
		}
	}
	return func(path string) bool { return paths[path] }, nil
}

// files adds the findings of the files at paths, as they are, and of the paths themselves.
func files(paths []string, terms []term, r *report) error {
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return masked(err, terms)
		}
		r.text(r.path(path, terms), data, terms)
	}
	return nil
}

// masked returns err with its path masked, as a finding's, where err is a *fs.PathError.
func masked(err error, terms []term) error {
	if failed, ok := errors.AsType[*fs.PathError](err); ok {
		path, _ := mask(failed.Path, terms)
		return fmt.Errorf("%s %s: %w", failed.Op, path, failed.Err)
	}
	return err
}

// history adds the findings of each object that git rev-list --objects revs lists in dir. It
// reads a commit or a tag as git cat-file -p prints it, author and committer included. It reads
// a tree's entries by their names, a line each, in git ls-tree's order. A file goes by its path,
// masked, and its blob's ID.
func history(dir, revs string, terms []term, r *report) error {
	out, err := gitOutput(dir, "rev-list", "--objects", revs, "--")
	if err != nil {
		return err
	}
	var ids []string
	paths := map[string]string{}
	for line := range strings.Lines(out) {
		id, path, _ := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
		ids, paths[id] = append(ids, id), path
	}
	if len(ids) == 0 {
		return nil
	}
	cmd := exec.Command("git", "-C", dir, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	err = objects(bufio.NewReader(pipe), len(ids), func(id, kind string, data []byte) {
		name := kind + " " + id[:12]
		switch kind {
		case "tree":
			data = entries(data, len(id)/2)
		case "blob":
			if path := paths[id]; path != "" {
				name, _ = mask(path, terms)
				name += "@" + id[:12]
			}
		}
		r.text(name, data, terms)
	})
	if waited := cmd.Wait(); err == nil && waited != nil {
		err = fmt.Errorf("git cat-file: %w: %s", waited, bytes.TrimSpace(stderr.Bytes()))
	}
	return err
}

// objects reads n objects of git cat-file --batch's output from out, and passes each to visit.
func objects(out *bufio.Reader, n int, visit func(id, kind string, data []byte)) error {
	for range n {
		header, err := out.ReadString('\n')
		if err != nil {
			return fmt.Errorf("git cat-file: %w", err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return fmt.Errorf("git cat-file: %q is no object's header", header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("git cat-file: %q is no object's header", header)
		}
		data := make([]byte, size+1)
		if _, err := io.ReadFull(out, data); err != nil {
			return fmt.Errorf("git cat-file: %w", err)
		}
		visit(fields[0], fields[1], data[:size])
	}
	return nil
}

// entries returns the names of a tree's entries, a line each, from the tree's data. An entry is a
// mode, a space and a name that a NUL ends, then an ID of size bytes.
func entries(data []byte, size int) []byte {
	var names []byte
	for len(data) > 0 {
		space, nul := bytes.IndexByte(data, ' '), bytes.IndexByte(data, 0)
		if space < 0 || nul < space || nul+1+size > len(data) {
			break
		}
		names = append(append(names, data[space+1:nul]...), '\n')
		data = data[nul+1+size:]
	}
	return names
}

// gitOutput runs git in dir, and returns its output. An error holds what git printed to stderr.
func gitOutput(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && len(exit.Stderr) > 0 {
		err = fmt.Errorf("git %s: %s", args[0], bytes.TrimSpace(exit.Stderr))
	}
	return string(out), err
}
