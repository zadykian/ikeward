package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runTests are command lines of valemirror, $DIR standing for a directory of Go files, and what
// valemirror writes and returns.
var runTests = []struct {
	name   string
	args   []string
	status int
	stdout string
	stderr string // a part of what valemirror writes to stderr
}{
	{
		name: "mirrors of the Markdown files, and of the Go files with prose", status: 0,
		args: []string{"$DIR/m", "$DIR/a.go", "$DIR/code.go", "$DIR/gone.go", "$DIR/b/a.go",
			"$DIR/doc.md"},
		stdout: "$DIR/m/0/a.go.md\n$DIR/m/3/a.go.md\n$DIR/m/4/doc.md\n",
	},
	{name: "no files", args: []string{"$DIR/m"}, status: 0},
	{name: "no directory", status: 2, stderr: "usage:"},
	{name: "-alerts with a file", args: []string{"-alerts", "$DIR/m", "$DIR/a.go"}, status: 2},
	{name: "-alerts without mirrors", args: []string{"-alerts", "$DIR/none"}, status: 2},
	{name: "-alerts with -switches", args: []string{"-alerts", "-switches", "$DIR/m"}, status: 2},
	{name: "-switches without files", args: []string{"-switches"}, status: 2, stderr: "usage:"},
	{name: "-switches on a file it cannot read", args: []string{"-switches", "$DIR"}, status: 2},
	{name: "a file it cannot read", args: []string{"$DIR/m", "$DIR"}, status: 2},
	{name: "help", args: []string{"-h"}, status: 0, stderr: "usage:"},
}

// writeSources writes the Go files that runTests read into dir.
func writeSources(t *testing.T, dir string) {
	t.Helper()
	for name, src := range map[string]string{
		"a.go":    "package a\n\n// F does it.\nfunc F() {}\n",
		"code.go": "package a\n\nvar b int\n",
		"b/a.go":  "package b\n\nvar c = 1 // so\n",
		"doc.md":  "# A\n\nIt costs $5, or $10.\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	for _, tt := range runTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeSources(t, dir)
			args := make([]string, len(tt.args))
			for i, arg := range tt.args {
				args[i] = strings.ReplaceAll(arg, "$DIR", dir)
			}
			var stdout, stderr strings.Builder
			status := run(args, strings.NewReader(""), &stdout, &stderr)
			want := strings.ReplaceAll(tt.stdout, "$DIR", dir)
			if status != tt.status || stdout.String() != want ||
				!strings.Contains(stderr.String(), tt.stderr) {
				t.Errorf("run(%q) = %d, stdout %q, stderr %q; want %d, %q, and %q in stderr",
					args, status, stdout.String(), stderr.String(), tt.status, want, tt.stderr)
			}
		})
	}
}

// TestRoundTrip writes mirrors, then maps alerts on them back to their Go and Markdown files.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSources(t, dir)
	mirrors := filepath.Join(dir, "m")
	a, b, doc := filepath.Join(dir, "a.go"), filepath.Join(dir, "b", "a.go"),
		filepath.Join(dir, "doc.md")
	var stdout, stderr strings.Builder
	if status := run([]string{mirrors, a, b, doc}, nil, &stdout, &stderr); status != 0 {
		t.Fatalf("run = %d, stderr %q", status, stderr.String())
	}
	for path, want := range map[string]string{
		filepath.Join(mirrors, "1", "a.go.md"): "\n\nso\n",
		filepath.Join(mirrors, "2", "doc.md"):  "# A\n\nIt costs ¤5, or ¤10.\n",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Errorf("the mirror %s is %q, want %q", path, data, want)
		}
	}
	alerts := filepath.Join(mirrors, "0", "a.go.md") + ":3:5:Rule:A message.\n" +
		filepath.Join(mirrors, "1", "a.go.md") + ":3:1:Rule:A message.\n" +
		filepath.Join(mirrors, "2", "doc.md") + ":3:10:Rule:A message.\n" +
		"README.md:1:1:Rule:A message.\n"
	stdout.Reset()
	if status := run([]string{"-alerts", mirrors}, strings.NewReader(alerts), &stdout,
		&stderr); status != 0 {
		t.Fatalf("run -alerts = %d, stderr %q", status, stderr.String())
	}
	want := a + ":3:8:Rule:A message.\n" + b + ":3:14:Rule:A message.\n" +
		doc + ":3:10:Rule:A message.\n" + "README.md:1:1:Rule:A message.\n"
	if stdout.String() != want {
		t.Errorf("run -alerts wrote %q, want %q", stdout.String(), want)
	}
}
