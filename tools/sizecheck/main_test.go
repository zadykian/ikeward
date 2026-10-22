package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain unsets the GIT_* variables git sets for hooks and rebase --exec, which would point
// the tests' git at the repository they run in. HOME and XDG_CONFIG_HOME go to an empty directory,
// and git reads no system config, so that no gitignore of the user's hides a test's file.
func TestMain(m *testing.M) {
	os.Exit(isolated(m))
}

// isolated runs the tests with git's environment and configuration set apart, and returns their
// exit status.
func isolated(m *testing.M) int {
	home, err := os.MkdirTemp("", "sizecheck-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for _, variable := range os.Environ() {
		if name, _, _ := strings.Cut(variable, "="); strings.HasPrefix(name, "GIT_") {
			err = errors.Join(err, os.Unsetenv(name))
		}
	}
	err = errors.Join(err, os.Setenv("HOME", home), os.Setenv("XDG_CONFIG_HOME", home),
		os.Setenv("GIT_CONFIG_NOSYSTEM", "1"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	status := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return status
}

// repository makes a git repository of files, by their path from it, all added to the index.
func repository(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, files)
	git(t, dir, "add", "-A")
	return dir
}

// git runs git in dir.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// write writes files in dir, by their path from it; "" for contents removes the file.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for path, contents := range files {
		path = filepath.Join(dir, filepath.FromSlash(path))
		if contents == "" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var runErrorTests = []struct {
	name     string
	files    map[string]string // the repository's files; nil for a directory git does not know
	args     []string
	status   int
	inStderr string
}{
	{
		name: "an unknown flag", files: map[string]string{}, args: []string{"-x"},
		status: 2, inStderr: "flag provided but not defined: -x\nusage: go run ./tools/sizecheck",
	},
	{
		name: "help", files: map[string]string{}, args: []string{"-h"},
		status: 0, inStderr: "usage: go run ./tools/sizecheck [-scripts] [FILE...]",
	},
	{name: "not a repository", status: 2, inStderr: "sizecheck: git ls-files: fatal:"},
	{
		name: "-scripts where git lists no script", files: map[string]string{"a.go": "package a\n"},
		args: []string{"-scripts"}, status: 2,
		inStderr: "sizecheck: no shell script among the files git lists",
	},
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range runErrorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tt.files != nil {
				dir = repository(t, tt.files)
			}
			var stdout, stderr strings.Builder
			status := run(dir, tt.args, &stdout, &stderr)
			if status != tt.status || !strings.Contains(stderr.String(), tt.inStderr) {
				t.Errorf("status %d, stderr %q; want %d and %q in it", status, stderr.String(),
					tt.status, tt.inStderr)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout %q, want nothing", stdout.String())
			}
		})
	}
}
