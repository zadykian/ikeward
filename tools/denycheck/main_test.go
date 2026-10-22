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
// git reads no system config, and the tests' commits get an author.
func TestMain(m *testing.M) {
	os.Exit(isolated(m))
}

// isolated runs the tests with git's environment and configuration set apart, and returns their
// exit status.
func isolated(m *testing.M) int {
	home, err := os.MkdirTemp("", "denycheck-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for _, variable := range os.Environ() {
		if name, _, _ := strings.Cut(variable, "="); strings.HasPrefix(name, "GIT_") {
			err = errors.Join(err, os.Unsetenv(name))
		}
	}
	for name, value := range map[string]string{"HOME": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.org",
		"GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.org"} {
		err = errors.Join(err, os.Setenv(name, value))
	}
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

// list is the tests' list of made-up terms, numbered by their lines: 1, 4, 5 and 6.
const list = "acme-widgets\n# A comment, then a blank line.\n\nContoso\n198.51.100\n" +
	"fabrikam%20labs\n"

// environ returns a getenv of the variables in env, which the process's own leave alone.
func environ(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

// withList is a getenv whose DENYLIST is list.
var withList = environ(map[string]string{"DENYLIST": list})

// repository makes a git repository of files, by their path from it, all added to the index.
func repository(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, files)
	git(t, dir, "add", "-A")
	return dir
}

// git runs git in dir, and returns its output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// write writes files in dir, by their path from it.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for path, contents := range files {
		path = filepath.Join(dir, filepath.FromSlash(path))
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
	args     []string
	status   int
	inStderr string
}{
	{
		name: "an unknown flag", args: []string{"-x"}, status: 2,
		inStderr: "flag provided but not defined: -x\nusage: go run ./tools/denycheck",
	},
	{name: "help", args: []string{"-h"}, inStderr: "usage: go run ./tools/denycheck [-require]"},
	{
		name: "two modes", args: []string{"-files", "-stdin", "body", "a"}, status: 2,
		inStderr: "denycheck: -history, -files and -stdin go alone",
	},
	{
		name: "-history with a FILE", args: []string{"-history", "HEAD", "a"}, status: 2,
		inStderr: "denycheck: -history and -stdin take no FILE",
	},
	{
		name: "-files without a FILE", args: []string{"-files"}, status: 2,
		inStderr: "denycheck: -files takes a FILE at least",
	},
	{name: "not a repository", status: 2, inStderr: "denycheck: git ls-files: fatal:"},
	{
		name: "a revision git does not know", args: []string{"-history", "nosuch"}, status: 2,
		inStderr: "denycheck: git rev-list: fatal:",
	},
	{
		name: "a FILE gone", args: []string{"-files", "nosuch"}, status: 2,
		inStderr: "denycheck: open nosuch: no such file or directory",
	},
	{
		name: "a FILE gone, whose path holds a term", args: []string{"-files", "a/Contoso.bin"},
		status: 2, inStderr: "denycheck: open a/[term 4].bin: no such file or directory",
	},
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range runErrorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr strings.Builder
			status := run(t.TempDir(), tt.args, withList, strings.NewReader(""), &stdout, &stderr)
			if status != tt.status || !strings.Contains(stderr.String(), tt.inStderr) {
				t.Errorf("status %d, stderr %q; want %d and %q in it", status, stderr.String(),
					tt.status, tt.inStderr)
			}
			if stdout.Len() != 0 || strings.Contains(stderr.String(), "Contoso") {
				t.Errorf("stdout %q, stderr %q; want nothing, and no term", stdout.String(),
					stderr.String())
			}
		})
	}
}
