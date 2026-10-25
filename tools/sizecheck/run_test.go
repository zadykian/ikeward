package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lines returns text of n lines.
func lines(n int) string {
	return strings.Repeat("x\n", n)
}

// runSteps is a session of sizecheck in one repository, each step on what those before it left.
// $DIR in args stands for the repository's directory.
var runSteps = []struct {
	name   string
	write  map[string]string // files to write before the step; "" removes one
	args   []string
	status int
	stdout string
}{
	{
		name: "files over the caps", status: 1,
		stdout: "big.md:1: 303 lines, over the cap of 300\n" +
			"bin/hook:1: 301 lines, over the cap of 300\n" +
			"styles/a/long.yml:1: 400 lines, over the cap of 300\n",
	},
	{
		name: "a FILE alone", args: []string{"bin/hook"}, status: 1,
		stdout: "bin/hook:1: 301 lines, over the cap of 300\n",
	},
	{
		name: "an absolute FILE", args: []string{"$DIR/bin/hook"}, status: 1,
		stdout: "bin/hook:1: 301 lines, over the cap of 300\n",
	},
	{
		name: "FILEs out of the repository, ignored, vendored, of another kind, missing or not files",
		args: []string{"../big.md", "ignored/big.md", ".vale/styles/a/long.yml", "notes.txt",
			"missing.md", "nested", "nested/a.md", "linked"},
	},
	{
		name: "the shell scripts, hidden or of another extension", args: []string{"-scripts"},
		stdout: ".probe.sh\nbin/hook\ntools/hook.txt\n",
	},
	{
		name: "the shell scripts among FILEs", args: []string{"-scripts", "tools/hook.txt", "big.md"},
		stdout: "tools/hook.txt\n",
	},
	{
		name: "files fixed or gone",
		write: map[string]string{
			"big.md": lines(300), "bin/hook": "", "styles/a/long.yml": "",
		},
	},
	{
		name: "a new file over the cap", write: map[string]string{"ci.yaml": lines(301)}, status: 1,
		stdout: "ci.yaml:1: 301 lines, over the cap of 300\n",
	},
}

// TestRun runs sizecheck in a repository that holds, besides its files, a nested repository and
// a symlink to a directory, which git lists and sizecheck passes over.
func TestRun(t *testing.T) {
	t.Parallel()
	dir := repository(t, map[string]string{
		".gitignore":              "/ignored/\n",
		"small.go":                "// Package a is small.\npackage a\n",
		"big.md":                  lines(303),
		"bin/hook":                "#!/bin/sh\n" + lines(300),
		".probe.sh":               "echo probe\n",
		"tools/hook.txt":          "#!/bin/sh\necho hook\n",
		"tools/hook.py":           "#!/usr/bin/env python3\n",
		"notes.txt":               lines(400),
		".vale/styles/a/LICENSE":  "MIT\n",
		".vale/styles/a/long.yml": lines(400),
		"styles/a/LICENSE":        "MIT\n",
		"styles/a/long.yml":       lines(400),
		"ignored/big.md":          lines(400),
	})
	nested := filepath.Join(dir, "nested")
	write(t, nested, map[string]string{"a.md": lines(400)})
	git(t, nested, "init", "-q")
	if err := os.Symlink("styles", filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, step := range runSteps {
		write(t, dir, step.write)
		args := make([]string, len(step.args))
		for i, arg := range step.args {
			args[i] = strings.ReplaceAll(arg, "$DIR", dir)
		}
		var stdout, stderr strings.Builder
		status := run(dir, args, &stdout, &stderr)
		if status != step.status || stdout.String() != step.stdout {
			t.Errorf("%s: status %d, stdout %q, stderr %q; want %d and %q", step.name, status,
				stdout.String(), stderr.String(), step.status, step.stdout)
		}
	}
}
