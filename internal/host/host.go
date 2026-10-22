// Package host is how ikeward reaches the system it runs on: the files under a root directory, the
// commands it runs, and strongSwan. It reaches the kernel's addresses, routes and nftables, a DNS
// server, the system's resolver, the terminal, the clock, and the program it runs as. The commands'
// code reaches each through a small interface of its own, which unit tests replace with a fake.
// This package's tests touch a temporary directory, in-memory pipes, and a fake kernel that
// answers nftables' batches and reads. They run sh alone, through Commands, and no command that
// reaches the network or the VPN.
package host

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Root is the files under a directory, "/" on a live system, each named by its absolute path on
// the system. Errors leave out the file's name, which the caller gives.
type Root string

// name is the file at path, a clean, absolute path, under r.
func (r Root) name(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("%q is not a clean, absolute path", path)
	}
	return filepath.Join(string(r), path), nil
}

// Read returns the file at path and its permission bits. Where there is no file, the error wraps
// fs.ErrNotExist.
func (r Root) Read(path string) ([]byte, fs.FileMode, error) {
	name, err := r.name(path)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, 0, reason(err)
	}
	defer f.Close() //nolint:errcheck // read only: closing loses nothing
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("not a regular file")
	}
	var data []byte
	if err == nil {
		data, err = io.ReadAll(f)
	}
	if err != nil {
		return nil, 0, reason(err)
	}
	return data, info.Mode().Perm(), nil
}

// Write replaces the file at path with data and mode, so that no one reads it half written. A
// temporary file beside it, which only its owner may read until it takes mode, is renamed over it.
// A missing directory is made 0755, or 0700 where it holds the file and the file is root's alone.
// Modes are exact, whatever the umask: networkd, for one, reads its settings as a user of its own.
func (r Root) Write(path string, data []byte, mode fs.FileMode) error {
	name, err := r.name(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(name)
	dirMode := fs.FileMode(0o755)
	if mode&0o077 == 0 {
		dirMode = 0o700
	}
	if err := mkdirs(dir, dirMode); err != nil {
		return reason(err)
	}
	// The name ends with no extension that strongSwan, systemd or networkd reads.
	temp, err := os.CreateTemp(dir, "."+filepath.Base(name)+".ikeward-*")
	if err != nil {
		return reason(err)
	}
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Chmod(mode)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), name)
	}
	if err != nil {
		_ = os.Remove(temp.Name()) //nolint:errcheck // best effort: the write's error is returned
		return reason(err)
	}
	return nil
}

// mkdirs makes dir with mode, and its missing parents with 0755, where dir is missing.
func mkdirs(dir string, mode fs.FileMode) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := mkdirs(parent, 0o755); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, mode); err != nil {
		return err
	}
	return os.Chmod(dir, mode)
}

// Remove removes the file at path. Where there is none, the error wraps fs.ErrNotExist.
func (r Root) Remove(path string) error {
	name, err := r.name(path)
	if err != nil {
		return err
	}
	return reason(os.Remove(name))
}

// reason is err without the operation and file name that *fs.PathError and *os.LinkError add. It
// also drops the operation, network and address that *net.OpError and *os.SyscallError add to a
// dial's.
func reason(err error) error {
	for {
		if e, ok := errors.AsType[*fs.PathError](err); ok {
			err = e.Err
		} else if e, ok := errors.AsType[*os.LinkError](err); ok {
			err = e.Err
		} else if e, ok := errors.AsType[*net.OpError](err); ok {
			err = e.Err
		} else if e, ok := errors.AsType[*os.SyscallError](err); ok {
			err = e.Err
		} else {
			return err
		}
	}
}

// Commands runs programs as ikeward's commands need them: with no input, and in ikeward's
// environment with DEBIAN_FRONTEND=noninteractive, so that apt-get asks nothing.
type Commands struct{}

// Run runs the program name with args and returns what it printed on stdout. Where it fails, the
// error says how, and what it printed on stderr.
func (Commands) Run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return stdout.String(), fmt.Errorf("%w: %s", err, said)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

// RunsAs returns the program ikeward runs as, and whether that is the file at path, or the one a
// link at path names. Where there is no file at path, the answer is no.
func RunsAs(path string) (string, bool, error) {
	running, err := os.Executable()
	if err != nil {
		return "", false, err
	}
	program, err := os.Stat(running)
	if err != nil {
		return running, false, reason(err)
	}
	want, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return running, false, nil
	case err != nil:
		return running, false, fmt.Errorf("%s: %w", path, reason(err))
	}
	return running, os.SameFile(program, want), nil
}
