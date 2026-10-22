package host

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// checkFile fails t unless the file name holds data with mode.
func checkFile(t *testing.T, name string, data string, mode fs.FileMode) {
	t.Helper()
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data || info.Mode().Perm() != mode {
		t.Errorf("%s: mode %04o and %q, want mode %04o and %q", name, info.Mode().Perm(), got,
			mode, data)
	}
}

// checkDir fails t unless dir has mode and holds the files named.
func checkDir(t *testing.T, dir string, mode fs.FileMode, names ...string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Errorf("%s: mode %04o, want %04o", dir, info.Mode().Perm(), mode)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if strings.Join(got, " ") != strings.Join(names, " ") {
		t.Errorf("%s holds %q, want %q", dir, got, names)
	}
}

// Write makes the directories, gives them and the file their modes exactly, whatever the umask,
// and leaves no temporary file; Read reads it back, and Remove removes it.
func TestRoot(t *testing.T) {
	umask := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(umask) })
	dir := t.TempDir()
	root := Root(dir)
	if err := root.Write("/etc/a/b.conf", []byte("one\n"), 0o664); err != nil {
		t.Fatal(err)
	}
	if err := root.Write("/etc/a/private/key.pem", []byte("key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkDir(t, filepath.Join(dir, "etc"), 0o755, "a")
	checkDir(t, filepath.Join(dir, "etc", "a"), 0o755, "b.conf", "private")
	checkDir(t, filepath.Join(dir, "etc", "a", "private"), 0o700, "key.pem")
	checkFile(t, filepath.Join(dir, "etc", "a", "b.conf"), "one\n", 0o664)
	checkFile(t, filepath.Join(dir, "etc", "a", "private", "key.pem"), "key\n", 0o600)

	// Another write replaces the file, and its mode.
	if err := root.Write("/etc/a/b.conf", []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkFile(t, filepath.Join(dir, "etc", "a", "b.conf"), "two\n", 0o644)
	checkDir(t, filepath.Join(dir, "etc", "a"), 0o755, "b.conf", "private")

	data, mode, err := root.Read("/etc/a/b.conf")
	if err != nil || string(data) != "two\n" || mode != 0o644 {
		t.Errorf("Read: %q, mode %04o, %v; want %q, mode 0644", data, mode, err, "two\n")
	}
	if err := root.Remove("/etc/a/b.conf"); err != nil {
		t.Fatal(err)
	}
	checkDir(t, filepath.Join(dir, "etc", "a"), 0o755, "private")
}

func TestRootErrors(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dir", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"Read missing", read(root, "/missing"), "no such file or directory"},
		{"Read directory", read(root, "/dir"), "not a regular file"},
		{"Read relative", read(root, "etc/x"), `"etc/x" is not a clean, absolute path`},
		{"Write unclean", root.Write("/etc/../x", nil, 0o644),
			`"/etc/../x" is not a clean, absolute path`},
		{"Write under a file", root.Write("/file/a/b", nil, 0o644), "not a directory"},
		{"Write deep under a file", root.Write("/file/a/b/c", nil, 0o644), "not a directory"},
		{"Write beside a file", root.Write("/file/a", nil, 0o644), "not a directory"},
		{"Write over a directory", root.Write("/dir", nil, 0o644), "file exists"},
		{"Write in a long name", root.Write("/dir/"+strings.Repeat("a", 256)+"/b", nil, 0o644),
			"file name too long"},
		{"Remove missing", root.Remove("/missing"), "no such file or directory"},
		{"Remove relative", root.Remove("x"), `"x" is not a clean, absolute path`},
		{"Remove a full directory", root.Remove("/dir"), "directory not empty"},
	} {
		if test.err == nil || test.err.Error() != test.want {
			t.Errorf("%s: %v, want %s", test.name, test.err, test.want)
		}
	}
	if err := read(root, "/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read of a missing file: %v, want fs.ErrNotExist", err)
	}
	if err := root.Remove("/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove of a missing file: %v, want fs.ErrNotExist", err)
	}
	// The write that failed over a directory left no temporary file.
	checkDir(t, dir, dirMode(t, dir), "dir", "file")
}

// read is Read's error.
func read(root Root, path string) error {
	_, _, err := root.Read(path)
	return err
}

// dirMode is the mode of dir, which t.TempDir sets.
func dirMode(t *testing.T, dir string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestCommands(t *testing.T) {
	out, err := Commands{}.Run("sh", "-c", `echo "$DEBIAN_FRONTEND"; cat`)
	if err != nil || out != "noninteractive\n" {
		t.Errorf("Run: %q, %v; want %q and no input", out, err, "noninteractive\n")
	}
	out, err = Commands{}.Run("sh", "-c", "echo out; echo ' said  ' >&2; exit 3")
	if want := "exit status 3: said"; err == nil || err.Error() != want || out != "out\n" {
		t.Errorf("Run: %q, %v; want %q and %s", out, err, "out\n", want)
	}
	_, err = Commands{}.Run("sh", "-c", "exit 4")
	if want := "exit status 4"; err == nil || err.Error() != want {
		t.Errorf("Run: %v, want %s", err, want)
	}
	_, err = Commands{}.Run(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Run of a missing program: %v, want fs.ErrNotExist", err)
	}
}

// ikeward runs as the test's program, which a link may name, and not as another file.
func TestRunsAs(t *testing.T) {
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	link, other := filepath.Join(dir, "link"), filepath.Join(dir, "other")
	if err := os.Symlink(program, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		same bool
	}{{program, true}, {link, true}, {other, false}, {filepath.Join(dir, "missing"), false}} {
		running, same, err := RunsAs(test.path)
		if running != program || same != test.same || err != nil {
			t.Errorf("RunsAs(%s): %s, %v, %v; want %s, %v", test.path, running, same, err,
				program, test.same)
		}
	}
	if _, _, err := RunsAs(filepath.Join(other, "x")); err == nil ||
		!strings.HasSuffix(err.Error(), ": not a directory") {
		t.Errorf("RunsAs under a file: %v, want an error", err)
	}
}
