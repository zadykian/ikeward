package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// words make a sentence of 31 words, one past Vale's 30, with the word before them.
var words = strings.Repeat(" word", 30)

// The alerts tools/valecheck prints, but for the path.
const (
	sentenceAlert = "Microsoft.SentenceLength:Try to keep sentences short (< 30 words).\n"
	switchAlert   = "valemirror.Switch:A comment turns a gate off; rewrite the text instead.\n"
)

// valeSources are the files of valecheckTests, by their path in the test's directory.
var valeSources = map[string]string{
	"a.md":      "# A\n\nOne" + words + ".\n",
	"b.go":      "package b\n\n// F" + words + ".\nfunc F() {}\n",
	"quiet.go":  "package b\n\nvar c = 1\n",
	"notes.txt": "One" + words + ".\n",
	"switch.md": "# A\n\n<!-- vale off -->\nOne" + words + ".\n",
	"lines.md":  "# A\n\n<!--\nvale off\n-->\nOne" + words + ".\n",
	"rule.md":   "# A\n\n<!--\n  vale Microsoft.SentenceLength = NO\n-->\nOne" + words + ".\n",
	"quoted.md": "# A\n\nQuoted: `<!--`\nvale off -->\n\nOne" + words + ".\n",
}

// valecheckTests are command lines of tools/valecheck on valeSources, and what it does.
var valecheckTests = []struct {
	name     string
	args     []string
	status   int
	stdout   string
	inStderr string
}{
	{
		name: "Markdown and Go FILEs, the alerts at their own paths", args: []string{"a.md", "./b.go"},
		status: 1, stdout: "a.md:3:1:" + sentenceAlert + "b.go:3:4:" + sentenceAlert,
		inStderr: "the alerts above fail the check",
	},
	{
		name: "FILEs of another kind passed over", args: []string{"notes.txt", "a.md"}, status: 1,
		stdout: "a.md:3:1:" + sentenceAlert,
	},
	{name: "no Markdown or Go FILE", args: []string{"notes.txt"}},
	{name: "a Go FILE with no prose to mirror", args: []string{"quiet.go"}},
	{
		name: "a missing FILE", args: []string{"a.md", "gone.md"}, status: 2,
		inStderr: "no file gone.md",
	},
	{name: "a directory", args: []string{"."}, status: 2, inStderr: "no file ."},
	{
		name: "a switch, which fails the check though Vale heeds it", args: []string{"switch.md"},
		status: 1, stdout: "switch.md:3:1:" + switchAlert,
	},
	{
		name: "switches across lines, which Vale heeds too", args: []string{"lines.md", "rule.md"},
		status: 1, stdout: "lines.md:3:1:" + switchAlert + "rule.md:3:1:" + switchAlert,
	},
	{
		name: "a switch quoted in a code span", args: []string{"quoted.md"}, status: 1,
		stdout: "quoted.md:6:1:" + sentenceAlert,
	},
}

func TestValecheck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, valeSources)
	for _, tt := range valecheckTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := script(t, dir, environ(t), "valecheck", tt.args...)
			if r.status != tt.status || r.stdout != tt.stdout || !strings.Contains(r.stderr, tt.inStderr) {
				t.Errorf("tools/valecheck = %d, stdout\n%s\nstderr\n%s\nwant %d, stdout\n%s\nand %q in stderr",
					r.status, r.stdout, r.stderr, tt.status, tt.stdout, tt.inStderr)
			}
		})
	}
}

// TestValecheckListed checks the files git lists, untracked ones included: one whose name git
// would quote, and not one git ignores or one deleted from the work tree.
func TestValecheckListed(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := repository(t, env)
	write(t, dir, map[string]string{
		".gitignore":   "/ignored.md\n",
		"résumé.go":    "package a\n\n// F" + words + ".\nfunc F() {}\n",
		"docs/a.md":    "# A\n\nShort.\n",
		"gone.md":      "# Gone\n",
		"docs/new.md":  "# New\n\nOne" + words + ".\n",
		"ignored.md":   "# Ignored\n\nOne" + words + ".\n",
		"notes/a.txt":  "One" + words + ".\n",
		"quoted\".md":  "# Q\n\nOne" + words + ".\n",
		"tab\tname.go": "package a\n",
	})
	git(t, dir, env, "add", ".gitignore", "résumé.go", "docs/a.md", "gone.md")
	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	r := script(t, dir, env, "valecheck")
	want := "docs/new.md:3:1:" + sentenceAlert + "quoted\".md:3:1:" + sentenceAlert +
		"résumé.go:3:4:" + sentenceAlert
	if r.status != 1 || r.stdout != want {
		t.Errorf("tools/valecheck = %d, stdout\n%s\nstderr\n%s\nwant 1 and\n%s", r.status, r.stdout,
			r.stderr, want)
	}
	write(t, dir, map[string]string{"line\nbreak.md": "# A\n"})
	r = script(t, dir, env, "valecheck")
	if r.status != 2 || !strings.Contains(r.stderr, "a file's name holds a newline") {
		t.Errorf("tools/valecheck with a newline in a name = %d, stderr\n%s\nwant 2", r.status,
			r.stderr)
	}
}

// TestValecheckConfig checks that Vale reads .vale.ini alone: not VALE_CONFIG_PATH's config, a
// global one of the user's, or styles in VALE_STYLES_PATH.
func TestValecheckConfig(t *testing.T) {
	t.Parallel()
	dir, config := t.TempDir(), t.TempDir()
	write(t, dir, map[string]string{"a.md": valeSources["a.md"]})
	write(t, config, map[string]string{
		"empty.ini":          "MinAlertLevel = error\n",
		"xdg/vale/.vale.ini": "[*.md]\nBasedOnStyles = Vale\n",
		"styles/Microsoft/SentenceLength.yml": "extends: occurrence\nmessage: x\nscope: sentence\n" +
			"level: suggestion\nmax: 100\ntoken: \\b(\\w+)\\b\n",
	})
	env := append(environ(t), "VALE_CONFIG_PATH="+filepath.Join(config, "empty.ini"),
		"XDG_CONFIG_HOME="+filepath.Join(config, "xdg"),
		"VALE_STYLES_PATH="+filepath.Join(config, "styles"))
	r := script(t, dir, env, "valecheck", "a.md")
	if want := "a.md:3:1:" + sentenceAlert; r.status != 1 || r.stdout != want {
		t.Errorf("tools/valecheck = %d, stdout\n%s\nstderr\n%s\nwant 1 and\n%s", r.status, r.stdout,
			r.stderr, want)
	}
}

// TestValecheckFails checks that a tool that fails, here a go that cannot build tools/valemirror,
// gives exit status 2, apart from the alerts' 1.
func TestValecheckFails(t *testing.T) {
	t.Parallel()
	dir, bin := t.TempDir(), t.TempDir()
	write(t, dir, map[string]string{"a.md": valeSources["a.md"]})
	write(t, bin, map[string]string{"go": "#!/bin/sh\necho no go >&2\nexit 1\n"})
	if err := os.Chmod(filepath.Join(bin, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(environ(t), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := script(t, dir, env, "valecheck", "a.md")
	if r.status != 2 || !strings.Contains(r.stderr, "cannot build tools/valemirror") {
		t.Errorf("tools/valecheck = %d, stdout %q, stderr\n%s\nwant 2", r.status, r.stdout, r.stderr)
	}
}
