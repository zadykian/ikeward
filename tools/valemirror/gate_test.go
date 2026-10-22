package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gateWords make a sentence of 31 words, one past Vale's 30, with the word before them.
var gateWords = strings.Repeat(" word", 30)

// gateSources are a Markdown file and a Go file. In each, every rule of .vale.ini flags a
// sentence, and so does SentenceLength one that a pair of $, a tag or a lone backtick would hide.
// A directive and a code block of 31 words pass.
var gateSources = map[string]string{
	"a.md": "# A\n\nOne" + gateWords + ".\n\nIt is done.\n\nWe saw the abolishment of it.\n\n" +
		"It reads $A" + gateWords + " ($).\n",
	"a.go": "// Package a is short.\npackage a\n\n//go:generate" + gateWords + "\n\n" +
		"// F" + gateWords + ".\n//\n//\tF" + gateWords + "\nfunc F() {}\n\n" +
		"// G does it in order to go.\nfunc G() {}\n\n// H saw the abolishment of it.\nfunc H() {}\n\n" +
		"// I wraps it in <pre> for the page.\n// Then" + gateWords + ".\nfunc I() {}\n\n" +
		"// J takes a <value>" + gateWords + ".\nfunc J() {}\n\n" +
		"// K's `lone tick\n// is" + gateWords + ", then `code`.\nfunc K() {}\n",
}

// gateAlerts are the alerts on gateSources, by file, line, column and rule.
var gateAlerts = []string{
	"a.go:6:4:Microsoft.SentenceLength",
	"a.go:11:14:write-good.TooWordy",
	"a.go:14:14:proselint.Needless",
	"a.go:18:4:Microsoft.SentenceLength",
	"a.go:21:4:Microsoft.SentenceLength",
	"a.go:24:4:Microsoft.SentenceLength",
	"a.md:3:1:Microsoft.SentenceLength",
	"a.md:5:1:write-good.TooWordy",
	"a.md:7:12:proselint.Needless",
	"a.md:9:1:Microsoft.SentenceLength",
}

// TestValecheck runs tools/valecheck, and Vale with it, on gateSources. A rule that Vale skips,
// as it does one misspelled or gone without a word, fails it. So does a Vale that cannot run,
// such as one tools/run cannot download, with tools/valecheck's stderr.
func TestValecheck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var files []string
	for name, src := range gateSources {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	cmd := exec.Command("sh", append([]string{"tools/valecheck"}, files...)...)
	cmd.Dir = filepath.Join("..", "..")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	exit, ok := errors.AsType[*exec.ExitError](err)
	switch {
	case ok && exit.ExitCode() == 2:
		t.Fatalf("tools/valecheck could not run, exit status 2; stderr:\n%s", stderr.String())
	case !ok || exit.ExitCode() != 1:
		t.Fatalf("tools/valecheck: %v, want exit status 1 for its alerts; stdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	var got []string
	for line := range strings.Lines(stdout.String()) {
		field := strings.SplitN(strings.TrimPrefix(line, dir+string(filepath.Separator)), ":", 5)
		got = append(got, strings.Join(field[:min(len(field), 4)], ":"))
	}
	slices.Sort(got)
	if !slices.Equal(got, slices.Sorted(slices.Values(gateAlerts))) {
		t.Errorf("tools/valecheck printed\n%s\nwant the alerts\n%s\nstderr:\n%s", stdout.String(),
			strings.Join(gateAlerts, "\n"), stderr.String())
	}
}
