package hooks

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// sizecheck is the runs of sizecheck that lint-file.sh makes on every file at path: whether the
// file is a shell script, then its size.
func sizecheck(path string) [][]string {
	return [][]string{{"go", "run", "./tools/sizecheck", "-scripts", path},
		{"go", "run", "./tools/sizecheck", path}}
}

// secrets is the runs that lint-file.sh makes on every file at path, last: gitleaks, then
// denycheck.
func secrets(path string) [][]string {
	return [][]string{{"run", "gitleaks", "dir", "--config=.gitleaks.toml", "--ignore-gitleaks-allow",
		"--gitleaks-ignore-path=/dev/null", "--no-banner", "--redact", "--verbose", path},
		{"go", "run", "./tools/denycheck", path}}
}

// markdown is the runs that lint-file.sh makes on the Markdown file at path.
func markdown(path string) [][]string {
	return slices.Concat([][]string{{"valecheck", path},
		{"run", "lychee", "--offline", "--include-fragments", "--no-progress", path},
		{"run", "rumdl", "check", "--no-cache", "--deny-config-warnings", path}},
		sizecheck(path), secrets(path))
}

// workflow is the runs that lint-file.sh makes on the workflow at path, ShellCheck's fetch first.
var workflow = slices.Concat([][]string{{"run", "-path", "shellcheck"},
	{"run", "-path", "shellcheck"},
	{"run", "actionlint", "-shellcheck=/fake/shellcheck", "-pyflakes=", ".github/workflows/ci.yml"},
	{"run", "zizmor", "--strict-collection", "--offline", ".github/workflows/ci.yml"}},
	sizecheck(".github/workflows/ci.yml"), secrets(".github/workflows/ci.yml"))

// script is the runs that lint-file.sh makes on the shell script tools/hook.
var script = slices.Concat([][]string{sizecheck("tools/hook")[0],
	{"run", "shellcheck", "-s", "sh", "tools/hook"},
	{"run", "shfmt", "-ln", "posix", "-d", "-i", "4", "tools/hook"}, sizecheck("tools/hook")[1]},
	secrets("tools/hook"))

// fileTests are files that Claude wrote, by their path from the world's directory. After each,
// lint-file.sh makes the fakes' runs in the work tree dir, and exits with status, inStderr on its
// stderr.
var fileTests = []struct {
	name, file string
	indent     bool     // the JSON is indented
	fail       []string // the fakes' patterns to fail on
	dir        string
	runs       [][]string
	status     int
	inStderr   string
}{
	{name: "a Markdown file", file: "repo/docs/a.md", dir: "repo", runs: markdown("docs/a.md")},
	{
		name: "indented JSON", file: "repo/docs/a.md", indent: true, dir: "repo",
		runs: markdown("docs/a.md"),
	},
	{
		name: "a path with a space", file: "repo/docs/a b.md", dir: "repo",
		runs: markdown("docs/a b.md"),
	},
	// Read as it stands in the JSON, the path would name docs/a\"b.md.
	{name: "a path that JSON escapes", file: `repo/docs/a"b.md`},
	{name: "a file git ignores", file: "repo/.claude/plans/plan.md"},
	{name: "a file of another repository", file: "other/a.md"},
	{name: "a file of no repository", file: "outside/a.md"},
	{name: "a file gone", file: "repo/docs/gone.md"},
	{
		name: "a file of a linked work tree", file: "linked/docs/a.md", dir: "linked",
		runs: markdown("docs/a.md"),
	},
	{name: "a file of a branch from before the gates", file: "old/docs/a.md"},
	{
		name: "a finding", file: "repo/docs/a.md", fail: []string{"valecheck *"}, dir: "repo",
		runs: markdown("docs/a.md"), status: 2,
		inStderr: "tools/valecheck docs/a.md fails:\nvalecheck docs/a.md: a finding\n",
	},
	{
		name: "a Go file", file: "repo/pkg/a.go", dir: "repo",
		runs: slices.Concat([][]string{{"gofmt", "-l", "pkg/a.go"},
			{"run", "golangci-lint", "run", "--allow-serial-runners", "./pkg/"},
			{"valecheck", "pkg/a.go"}}, sizecheck("pkg/a.go"), secrets("pkg/a.go")),
	},
	{
		name: "a Go file gofmt lists", file: "repo/pkg/a.go", fail: []string{"gofmt -l *"},
		dir: "repo", status: 2, inStderr: "gofmt -l pkg/a.go lists it:\n",
		runs: slices.Concat([][]string{{"gofmt", "-l", "pkg/a.go"}, {"gofmt", "-d", "pkg/a.go"},
			{"run", "golangci-lint", "run", "--allow-serial-runners", "./pkg/"},
			{"valecheck", "pkg/a.go"}}, sizecheck("pkg/a.go"), secrets("pkg/a.go")),
	},
	{name: "a shell script, by its shebang", file: "repo/tools/hook", dir: "repo", runs: script},
	{
		name: "a shell script whose ShellCheck tools/run cannot fetch", file: "repo/tools/hook",
		fail: []string{"run shellcheck *"}, dir: "repo", runs: script, status: 2,
		inStderr: "tools/run shellcheck -s sh tools/hook fails:\n",
	},
	{name: "a workflow", file: "repo/.github/workflows/ci.yml", dir: "repo", runs: workflow},
	{
		name: "a secret", file: "repo/docs/a.md", fail: []string{"run gitleaks *"}, dir: "repo",
		runs: markdown("docs/a.md"), status: 2,
		inStderr: "tools/run gitleaks dir --config=.gitleaks.toml --ignore-gitleaks-allow",
	},
	{
		name: "an employer's term", file: "repo/docs/a.md", fail: []string{"go run ./tools/denycheck *"},
		dir: "repo", runs: markdown("docs/a.md"), status: 2,
		inStderr: "go run ./tools/denycheck docs/a.md fails:\n",
	},
	{
		name: "a workflow whose ShellCheck tools/run cannot fetch",
		file: "repo/.github/workflows/ci.yml", fail: []string{"run -path shellcheck"}, dir: "repo",
		runs: append([][]string{workflow[0]}, workflow[3:]...), status: 2,
		inStderr: "tools/run -path shellcheck fails:\nrun -path shellcheck: a finding\n",
	},
}

// postToolUse is the JSON that Claude Code pipes into the hook after a Write of path. The text
// written holds a quoted "file_path" key, of decoy, which the hook must not read.
func postToolUse(path, decoy string) map[string]any {
	return map[string]any{
		"session_id": "a", "transcript_path": "/a.jsonl", "cwd": "/",
		"hook_event_name": "PostToolUse", "tool_name": "Write",
		"tool_input": map[string]any{"file_path": path, "content": "# A\n"},
		"tool_response": map[string]any{"type": "update", "filePath": path,
			"content": `"file_path": "` + decoy + `"`},
	}
}

func TestLintFile(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	decoy := filepath.Join(w.repo, "pkg", "a.go")
	for _, tt := range fileTests {
		input := encode(t, postToolUse(filepath.Join(w.root, tt.file), decoy), tt.indent)
		r := w.hook(t, "lint-file.sh", w.root, input, tt.fail...)
		var want []run
		for _, args := range tt.runs {
			want = append(want, run{dir: filepath.Join(w.root, tt.dir), args: args})
		}
		if got := w.runs(t); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the fakes ran\n%q\nwant\n%q", tt.name, got, want)
		}
		if r.status != tt.status || !strings.Contains(r.stderr, tt.inStderr) || r.stdout != "" {
			t.Errorf("%s: status %d, stdout %q, stderr %q; want %d and %q", tt.name, r.status,
				r.stdout, r.stderr, tt.status, tt.inStderr)
		}
	}
}

// TestLintFileBeforeDenycheck runs lint-file.sh on a file of a branch from before denycheck, which
// has the gates but no tools/denycheck. Every gate runs but denycheck.
func TestLintFileBeforeDenycheck(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	git(t, w.linked, "rm", "-q", "-r", "tools/denycheck")
	git(t, w.linked, "commit", "-q", "-m", "chore: before denycheck")
	input := encode(t, postToolUse(filepath.Join(w.linked, "docs", "a.md"), ""), false)
	r := w.hook(t, "lint-file.sh", w.root, input)
	var want []run
	for _, args := range markdown("docs/a.md") {
		if !slices.Contains(args, "./tools/denycheck") {
			want = append(want, run{dir: w.linked, args: args})
		}
	}
	if got := w.runs(t); r.status != 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("status %d, stderr %q, and the fakes ran\n%q\nwant 0 and\n%q", r.status, r.stderr,
			got, want)
	}
}

// TestLintFileNoPath runs lint-file.sh after a tool that names no file.
func TestLintFileNoPath(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	input := encode(t, map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": "ls"}}, false)
	if r := w.hook(t, "lint-file.sh", w.repo, input); r.status != 0 || r.stderr != "" {
		t.Errorf("status %d, stderr %q; want 0 and nothing", r.status, r.stderr)
	}
	if runs := w.runs(t); len(runs) != 0 {
		t.Errorf("the fakes ran %q, want nothing", runs)
	}
}
