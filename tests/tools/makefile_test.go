package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// filesTests are values of the Makefile's FILES, and the part of make's stderr that refuses each,
// "" for one it takes. make -n only reads the Makefile and prints the recipe.
var filesTests = []struct {
	files, inStderr string
}{
	{files: "README.md"},
	{files: "README.md docs/checks.md"},
	{files: "docs", inStderr: "FILES names what is not a file: docs."},
	{files: "README.md gone.md docs/", inStderr: "FILES names what is not a file: gone.md docs/."},
}

func TestFiles(t *testing.T) {
	t.Parallel()
	for _, tt := range filesTests {
		r := command(t, root(t), environ(t), "make", "-n", "sizecheck", "FILES="+tt.files)
		want := "go run ./tools/sizecheck " + tt.files + "\n"
		switch {
		case tt.inStderr == "" && (r.status != 0 || r.stdout != want):
			t.Errorf("make -n sizecheck FILES=%q = %d, stdout %q, stderr %q; want 0 and %q",
				tt.files, r.status, r.stdout, r.stderr, want)
		case tt.inStderr != "" && (r.status != 2 || !strings.Contains(r.stderr, tt.inStderr)):
			t.Errorf("make -n sizecheck FILES=%q = %d, stderr %q; want 2 and %q", tt.files,
				r.status, r.stderr, tt.inStderr)
		}
	}
}

// TestMarkdown runs make lychee and rumdl in a repository whose tools/run prints what it gets. It
// gets the Markdown files git lists, untracked ones too, but those it ignores and those gone from
// disk. A non-ASCII name goes whole. With FILES, it gets the Markdown files among them alone. A
// name with a space, a tab or a quote fails both before tools/run runs.
func TestMarkdown(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := repository(t, env)
	write(t, dir, map[string]string{
		".gitignore": "/ignored.md\n", "a.md": "# A\n", "gone.md": "# B\n", "new.md": "# C\n",
		"résumé.md": "# R\n", "ignored.md": "# D\n", "notes.txt": "E\n",
		"tools/run": "#!/bin/sh\nprintf '%s\\n' \"$@\"\n",
	})
	if err := os.Chmod(filepath.Join(dir, "tools", "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, env, "add", ".gitignore", "a.md", "gone.md")
	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	makefile := filepath.Join(root(t), "Makefile")
	for files, want := range map[string][]string{
		"": {
			"a.md", "a.md", "lychee", "new.md", "new.md", "rumdl", "résumé.md", "résumé.md",
		},
		"notes.txt":        nil,
		"notes.txt new.md": {"lychee", "new.md", "new.md", "rumdl"},
	} {
		r := command(t, dir, env, "make", "-s", "-f", makefile, "lychee", "rumdl", "FILES="+files)
		var got []string
		for arg := range strings.FieldsSeq(r.stdout) {
			if !strings.HasPrefix(arg, "-") && arg != "check" {
				got = append(got, arg)
			}
		}
		slices.Sort(got)
		if r.status != 0 || !slices.Equal(got, want) {
			t.Errorf("make lychee rumdl FILES=%q = %d, stdout %q, stderr %q; want 0 and the tools "+
				"run on %q", files, r.status, r.stdout, r.stderr, want)
		}
	}
	for _, name := range []string{"two words.md", "tab\tname.md", `quote".md`} {
		write(t, dir, map[string]string{name: "# F\n"})
		r := command(t, dir, env, "make", "-s", "-f", makefile, "lychee", "rumdl")
		if r.status != 2 || r.stdout != "" ||
			!strings.Contains(r.stderr, "make: a name above holds a space, or a sign git quotes") {
			t.Errorf("make lychee rumdl with %q = %d, stdout %q, stderr %q; want 2 before the tools "+
				"run", name, r.status, r.stdout, r.stderr)
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// TestNoHead runs make gitleaks in a repository with a file but no commit, where gitleaks would
// scan no commit and pass. It fails before tools/run, here a stub, runs.
func TestNoHead(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := repository(t, env)
	write(t, dir, map[string]string{"a.md": "# A\n", "tools/run": "#!/bin/sh\necho ran\n"})
	if err := os.Chmod(filepath.Join(dir, "tools", "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := os.Symlink(filepath.Join(root(t), "tools", "profilecheck"),
		filepath.Join(dir, "tools", "profilecheck"))
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, env, "add", "a.md")
	r := command(t, dir, env, "make", "-s", "-f", filepath.Join(root(t), "Makefile"), "gitleaks")
	if r.status != 2 || r.stdout != "" ||
		!strings.Contains(r.stderr, "make gitleaks: no HEAD to scan") {
		t.Errorf("make gitleaks with no commit = %d, stdout %q, stderr %q; want 2 before tools/run",
			r.status, r.stdout, r.stderr)
	}
}

// TestUnreadable runs the gates that list files where git cannot read the repository. Each fails
// with git's message or the script's, before a tool runs on what it would have listed.
func TestUnreadable(t *testing.T) {
	t.Parallel()
	env := append(environ(t), "GIT_DIR="+filepath.Join(t.TempDir(), "none"))
	for target, inStderr := range map[string]string{
		"vet":      "sizecheck: git ls-files: fatal: not a git repository",
		"lychee":   "fatal: not a git repository",
		"rumdl":    "fatal: not a git repository",
		"gitleaks": "tools/profilecheck: git cannot read the work tree",
		"vale":     "tools/valecheck: git cannot list the files",
	} {
		r := command(t, root(t), env, "make", "-s", target)
		if r.status != 2 || !strings.Contains(r.stderr, inStderr) {
			t.Errorf("make %s = %d, stdout %q, stderr %q; want 2 and %q", target, r.status, r.stdout,
				r.stderr, inStderr)
		}
	}
}

// TestRunPath checks tools/run -path: a tool's directory in .cache/tools is named for its version
// and the start of its sum, and holds the executable.
func TestRunPath(t *testing.T) {
	t.Parallel()
	run := filepath.Join(root(t), "tools", "run")
	r := command(t, root(t), environ(t), run, "-path", "shfmt")
	path := strings.TrimSuffix(r.stdout, "\n")
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(root(t)) +
		`/\.cache/tools/shfmt-[0-9.]+-[0-9a-f]{12}/shfmt$`)
	if r.status != 0 || !pattern.MatchString(path) {
		t.Fatalf("tools/run -path shfmt = %d, stdout %q, stderr %q; want 0 and %s", r.status,
			r.stdout, r.stderr, pattern)
	}
	if info, err := os.Stat(path); err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("%s is no executable: %v", path, err)
	}
	for args, inStderr := range map[string]string{
		"":       "usage: tools/run NAME [ARG...] or tools/run -path NAME",
		"nosuch": "no tool nosuch",
	} {
		r := command(t, root(t), environ(t), run, strings.Fields(args)...)
		if r.status != 1 || !strings.Contains(r.stderr, inStderr) {
			t.Errorf("tools/run %s = %d, stderr %q; want 1 and %q", args, r.status, r.stderr,
				inStderr)
		}
	}
}

// fakeLogged is a go, gofmt or tools/run that logs its name and arguments to FAKE_LOG, a line a
// run. As go run ./tools/sizecheck -scripts, it prints the script hook.sh.
const fakeLogged = `#!/bin/sh
printf '%s %s\n' "${0##*/}" "$*" >>"$FAKE_LOG"
[ "$*" != "run ./tools/sizecheck -scripts" ] || echo hook.sh
`

// TestVet runs make vet in a repository whose go, gofmt and tools/run are fakes. ShellCheck and
// shfmt read the scripts as POSIX sh. gofmt reads the Go files git lists, untracked ones too, but
// none it ignores, as a work tree in .claude/worktrees, and none gone from disk.
func TestVet(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := repository(t, env)
	fakes, log := t.TempDir(), filepath.Join(t.TempDir(), "log")
	write(t, dir, map[string]string{
		".gitignore": "/.claude/worktrees/\n", "a.go": "package a\n", "b/c.go": "package b\n",
		"gone.go": "package a\n", "new.go": "package a\n", ".claude/worktrees/x/d.go": "package  x\n",
		"hook.sh": "#!/bin/sh\n", "tools/run": fakeLogged,
	})
	write(t, fakes, map[string]string{"go": fakeLogged, "gofmt": fakeLogged})
	for _, path := range []string{filepath.Join(dir, "tools", "run"), filepath.Join(fakes, "go"),
		filepath.Join(fakes, "gofmt")} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, env, "add", ".gitignore", "a.go", "b/c.go", "gone.go")
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}
	env = append(env, "PATH="+fakes+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_LOG="+log)
	r := command(t, dir, env, "make", "-s", "-f", filepath.Join(root(t), "Makefile"), "vet")
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go run ./tools/sizecheck -scripts", "run shellcheck -s sh hook.sh",
		"run shfmt -ln posix -d -i 4 hook.sh", "gofmt -l a.go b/c.go new.go", "go vet ./..."}
	var got []string
	for line := range strings.Lines(string(data)) {
		if fields := strings.Fields(line); fields[0] == "gofmt" {
			slices.Sort(fields[2:])
			line = strings.Join(fields, " ")
		}
		got = append(got, strings.TrimSuffix(line, "\n"))
	}
	if r.status != 0 || !slices.Equal(got, want) {
		t.Errorf("make vet = %d, stderr %q, and the fakes ran\n%q\nwant 0 and\n%q", r.status,
			r.stderr, got, want)
	}
}

// TestLintLocal checks that make lint-local runs the gates of make lint, in its order, but
// govulncheck, and zizmor with --offline, which make lint and make zizmor leave out. A rule's
// line may go on after a backslash.
func TestLintLocal(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(root(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	gates := map[string][]string{}
	for line := range strings.Lines(strings.ReplaceAll(string(data), "\\\n", " ")) {
		target, rest, ok := strings.Cut(line, ": ")
		if ok && (target == "lint" || target == "lint-local") && !strings.Contains(rest, "=") {
			gates[target] = strings.Fields(rest)
		}
	}
	want := slices.DeleteFunc(slices.Clone(gates["lint"]), func(gate string) bool {
		return gate == "govulncheck"
	})
	if len(want) == len(gates["lint"]) || !slices.Equal(gates["lint-local"], want) {
		t.Errorf("make lint-local runs %q, want %q: make lint's %q but govulncheck",
			gates["lint-local"], want, gates["lint"])
	}
	for _, tt := range []struct {
		target               string
		offline, govulncheck bool
	}{{"lint-local", true, false}, {"lint", false, true}, {"zizmor", false, false}} {
		r := command(t, root(t), environ(t), "make", "-n", tt.target)
		offline := strings.Contains(r.stdout, "tools/run zizmor --strict-collection --offline .")
		if r.status != 0 || offline != tt.offline ||
			strings.Contains(r.stdout, "govulncheck") != tt.govulncheck {
			t.Errorf("make -n %s = %d, stdout\n%s\nwant zizmor offline %v, govulncheck %v",
				tt.target, r.status, r.stdout, tt.offline, tt.govulncheck)
		}
	}
}

// TestDenycheck checks the Makefile's runs of tools/denycheck, which make -n prints. With
// DENYCHECK=true, as CI's workflows run make, none runs, so no other tool gets the list.
func TestDenycheck(t *testing.T) {
	t.Parallel()
	targets := []string{"-n", "check", "commits", "history"}
	r := command(t, root(t), environ(t), "make", targets...)
	for _, run := range []string{"go run ./tools/denycheck\n",
		"go run ./tools/denycheck -history origin/main..HEAD\n",
		"go run ./tools/denycheck -history --all\n"} {
		if r.status != 0 || !strings.Contains(r.stdout, run) {
			t.Errorf("make -n check commits history = %d, stdout\n%s\nwant %q in it", r.status,
				r.stdout, run)
		}
	}
	r = command(t, root(t), environ(t), "make", append(targets, "DENYCHECK=true")...)
	if r.status != 0 || strings.Contains(r.stdout, "tools/denycheck") ||
		!strings.Contains(r.stdout, "true -history --all\n") {
		t.Errorf("make -n check commits history DENYCHECK=true = %d, stdout\n%s\nwant no "+
			"denycheck", r.status, r.stdout)
	}
}
