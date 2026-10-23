// Package hooks checks Claude Code's hooks in .claude/hooks, fed the JSON that Claude Code pipes
// into them. They run in a scratch repository of the test's, with a copy of each hook. It holds a
// file git ignores, a linked work tree, and one on a branch from before the gates. Fakes stand in
// for make, go, gofmt, tools/run and tools/valecheck: each logs its run, and fails where the test
// asks. The hooks' PATH holds the fakes and the real tools the scripts run, nothing else, so no
// gate runs and nothing reaches the network. git reads no config of the user's or the system's.
package hooks

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTool logs each run to FAKE_LOG, a line of its directory, its name and its arguments, a tab
// between each. It fails with a finding where a line of FAKE_FAIL, a pattern, matches its name
// and arguments. As tools/run -path, it prints a path in /fake. As sizecheck -scripts, it prints
// the FILE whose first line starts with #!.
const fakeTool = `#!/bin/sh
set -f
printf '%s' "$(pwd)" >>"$FAKE_LOG"
printf '\t%s' "${0##*/}" "$@" >>"$FAKE_LOG"
printf '\n' >>"$FAKE_LOG"
line="${0##*/} $*"
IFS='
'
for pattern in ${FAKE_FAIL-}; do
    case $line in
    $pattern)
        printf '%s: a finding\n' "$line"
        exit 1
        ;;
    esac
done
case $line in
"run -path "*) printf '/fake/%s\n' "$2" ;;
"go run ./tools/sizecheck -scripts "*) head -n 1 -- "$4" | grep -q '^#!' && printf '%s\n' "$4" ;;
esac
exit 0
`

// fakeMake logs each run as fakeTool does. Where FAKE_FAIL is set, it fails as make -k does
// where two targets fail: a line for each, then the end.
const fakeMake = `#!/bin/sh
printf '%s' "$(pwd)" >>"$FAKE_LOG"
printf '\t%s' make "$@" >>"$FAKE_LOG"
printf '\n' >>"$FAKE_LOG"
[ -n "${FAKE_FAIL-}" ] || exit 0
echo 'a.go:1: a finding'
echo 'make: *** [Makefile:40: vet] Error 1' >&2
echo 'make: *** [Makefile:93: zizmor] Error 1' >&2
echo "make: Target 'lint-local' not remade because of errors." >&2
exit 2
`

// realTools are those the hooks and the fakes run from the PATH.
var realTools = []string{"basename", "cat", "dirname", "git", "grep", "head", "mktemp", "rm",
	"sed", "tail", "tr", "xargs"}

// world is a scratch repository for the hooks. repo is its main work tree, linked a linked one
// on the branch feature, and old one on the branch old, which has neither .vale.ini nor
// make lint-local. other is another repository, and outside a directory of no repository.
type world struct {
	root, repo, linked, old, other, outside, bin, log string
}

// run is a fake's run: the directory it ran in, and its name and arguments.
type run struct {
	dir  string
	args []string
}

// result is how a hook's run ended.
type result struct {
	status         int
	stdout, stderr string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &world{root: root, repo: filepath.Join(root, "repo"), linked: filepath.Join(root, "linked"),
		old: filepath.Join(root, "old"), other: filepath.Join(root, "other"),
		outside: filepath.Join(root, "outside"), bin: filepath.Join(root, "bin"),
		log: filepath.Join(root, "fake.log")}
	files := map[string]string{
		".gitignore": "/.claude/plans/\n", ".vale.ini": "StylesPath = .vale/styles\n",
		"Makefile": "lint-local: vet\n", "docs/a.md": "# A\n", "docs/a b.md": "# A B\n",
		"docs/a\"b.md": "# A\n", `docs/a\"b.md`: "# A\n", "pkg/a.go": "package a\n",
		"tools/hook": "#!/bin/sh\n", ".github/workflows/ci.yml": "name: ci\n",
		"tools/denycheck/main.go": "package main\n",
		".claude/plans/plan.md":   "# Plan\n",
	}
	write(t, w.repo, files)
	for _, hook := range []string{"lint-branch.sh"} {
		src, err := os.ReadFile(filepath.Join("..", "..", ".claude", "hooks", hook))
		if err != nil {
			t.Fatal(err)
		}
		executable(t, filepath.Join(w.repo, ".claude", "hooks", hook), string(src))
	}
	for _, name := range []string{"run", "valecheck"} {
		executable(t, filepath.Join(w.repo, "tools", name), fakeTool)
	}
	write(t, w.other, map[string]string{".vale.ini": "", "Makefile": "lint-local:\n", "a.md": "# A\n"})
	write(t, w.outside, map[string]string{"a.md": "# A\n"})
	git(t, w.repo, "init", "-q", "-b", "main")
	git(t, w.repo, "add", "-A")
	git(t, w.repo, "commit", "-q", "-m", "chore: the repository")
	git(t, w.repo, "worktree", "add", "-q", "-b", "feature", w.linked)
	git(t, w.repo, "worktree", "add", "-q", "-b", "old", w.old)
	git(t, w.old, "rm", "-q", ".vale.ini", "Makefile")
	git(t, w.old, "commit", "-q", "-m", "chore: before the gates")
	git(t, w.other, "init", "-q", "-b", "main")
	w.tools(t)
	return w
}

// tools fills the hooks' PATH: the fakes, and links to the real tools.
func (w *world) tools(t *testing.T) {
	t.Helper()
	for name, script := range map[string]string{"go": fakeTool, "gofmt": fakeTool,
		"make": fakeMake} {
		executable(t, filepath.Join(w.bin, name), script)
	}
	for _, name := range realTools {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("%s is not installed", name)
		}
		if err := os.Symlink(path, filepath.Join(w.bin, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// hook runs the main work tree's .claude/hooks/NAME in dir with input on stdin, as Claude Code
// does, and returns how it ended. Where fail is not empty, the fakes fail on its patterns.
func (w *world) hook(t *testing.T, name, dir, input string, fail ...string) result {
	t.Helper()
	cmd := exec.Command(filepath.Join(w.repo, ".claude", "hooks", name))
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(input)
	cmd.Env = []string{"PATH=" + w.bin, "HOME=" + w.root, "FAKE_LOG=" + w.log,
		"FAKE_FAIL=" + strings.Join(fail, "\n"), "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1"}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		r.status = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r
}

// runs returns the fakes' runs since the last call, and clears their log.
func (w *world) runs(t *testing.T) []run {
	t.Helper()
	data, err := os.ReadFile(w.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(w.log); err != nil {
		t.Fatal(err)
	}
	var runs []run
	for line := range strings.Lines(string(data)) {
		fields := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
		runs = append(runs, run{dir: fields[0], args: fields[1:]})
	}
	return runs
}

// encode returns v as JSON, compact as Claude Code sends it, or indented.
func encode(t *testing.T, v any, indent bool) string {
	t.Helper()
	data, err := json.Marshal(v)
	if indent {
		data, err = json.MarshalIndent(v, "", "  ")
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// environ is the environment of the test's own git: git's and the user's config set apart, and an
// author for the commits.
func environ() []string {
	var env []string
	for _, variable := range os.Environ() {
		if name, _, _ := strings.Cut(variable, "="); !strings.HasPrefix(name, "GIT_") {
			env = append(env, variable)
		}
	}
	return append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.org", "GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.org")
}

// git runs git in dir. Where git fails, so does the test.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
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

// executable writes the script at path, executable.
func executable(t *testing.T, path, script string) {
	t.Helper()
	write(t, filepath.Dir(path), map[string]string{filepath.Base(path): script})
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
