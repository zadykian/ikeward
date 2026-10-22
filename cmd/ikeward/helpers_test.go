package main

import (
	"os"
	"path/filepath"
	"testing"

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
