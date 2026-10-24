package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/host"
	"github.com/zadykian/ikeward/internal/profile"
	"github.com/zadykian/ikeward/internal/profile/profiletest"
)

// installArgs are install's arguments with the profile's files in dir, the remote network's
// values, then more.
func installArgs(dir string, more ...string) []string {
	args := append([]string{"install"}, profileArgs(dir)...)
	args = append(args, "--dns", "192.0.2.10", "--domain", "example.test", "--floor",
		"10.88.0.0/16")
	return append(args, more...)
}

// profileArgs are install's flags for the profile's files in dir, client.p12 and client.sswan.
func profileArgs(dir string) []string {
	return []string{"--p12", filepath.Join(dir, "client.p12"), "--sswan",
		filepath.Join(dir, "client.sswan")}
}

// writeProfile writes the files of a profile issued for client, whose .p12 has password, into a
// directory of the test's, and returns its path.
func writeProfile(t *testing.T, password string) string {
	t.Helper()
	dir := t.TempDir()
	issued := profiletest.Files(t, "client", password)
	for _, f := range []profile.File{issued.P12, issued.SSwan} {
		if err := os.WriteFile(filepath.Join(dir, f.Name), f.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// installedRoot is a temporary root directory where install wrote files.SettingsFile.
func installedRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := host.Root(root).Write(files.SettingsFile, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// installed are the files under root, each as the absolute path it stands for, sorted.
func (f *fakeSystem) installed(t *testing.T) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(f.root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(f.root, name)
		paths = append(paths, "/"+filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	return paths
}

// mode is the mode of the file at path under root.
func (f *fakeSystem) mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(filepath.Join(f.root, path))
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
