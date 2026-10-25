// Package tools checks the scripts in tools/ that make lint runs, and the Makefile's recipes that
// run them, in scratch repositories of the tests' own. tools/valecheck and tools/profilecheck read
// their files. gitleaks reads a profile that profiletest makes, with .gitleaks.toml. tools/run
// fetches Vale, gitleaks and shfmt into the repository's .cache/tools, as make lint does. git and
// Vale read no config of the user's or the system's.
package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// result is what a command did: its exit status, and what it wrote to stdout and stderr.
type result struct {
	status         int
	stdout, stderr string
}

// root returns the repository's root, which holds the Makefile and tools/.
func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// environ returns the environment for git, make and the scripts. It drops the variables that git
// sets for a hook and those that make passes on. git and Vale read no config but the repository's,
// and the commits get an author.
func environ(t *testing.T) []string {
	t.Helper()
	var env []string
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		switch {
		case strings.HasPrefix(name, "GIT_"), strings.HasPrefix(name, "VALE_"),
			strings.HasPrefix(name, "MAKE"), name == "MFLAGS", name == "XDG_CONFIG_HOME":
		default:
			env = append(env, variable)
		}
	}
	return append(env, "XDG_CONFIG_HOME="+t.TempDir(), "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.org",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.org")
}

// command runs name with args in dir, in env, and returns what it did. A command that cannot
// start fails the test.
func command(t *testing.T, dir string, env []string, name string, args ...string) result {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		r.status = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return r
}

// script runs the repository's tools/NAME with sh in dir.
func script(t *testing.T, dir string, env []string, name string, args ...string) result {
	t.Helper()
	path := filepath.Join(root(t), "tools", name)
	return command(t, dir, env, "sh", append([]string{path}, args...)...)
}

// git runs git in dir. Where git fails, so does the test.
func git(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	r := command(t, dir, env, "git", args...)
	if r.status != 0 {
		t.Fatalf("git %s: exit status %d\n%s", strings.Join(args, " "), r.status, r.stderr)
	}
}

// repository makes an empty git repository, on the branch main, in a directory of the test's.
func repository(t *testing.T, env []string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, env, "init", "-q", "-b", "main")
	return dir
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
