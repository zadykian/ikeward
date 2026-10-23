package tools

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeGate stands for go, gofmt, tools/run or tools/valecheck. It logs its name and arguments to
// FAKE_LOG, a line a run, and fails where the line matches the pattern FAKE_FAIL. Run as
// sizecheck -scripts on a file whose name ends in .sh, it prints that name.
const fakeGate = `#!/bin/sh
run="${0##*/} $*"
printf '%s\n' "$run" >>"$FAKE_LOG"
case $run in
"go run ./tools/sizecheck -scripts "*.sh) echo "${run##* }" ;;
esac
case $run in
$FAKE_FAIL) exit 1 ;;
esac
`

// lintFile is the runs that make lint-file makes on the file at path. First come those of its
// type, then a shell script's where sizecheck finds one. Then come the size caps, gitleaks and
// denycheck.
func lintFile(path string, ofType ...string) []string {
	runs := append(slices.Clone(ofType), "go run ./tools/sizecheck -scripts "+path)
	if strings.HasSuffix(path, ".sh") {
		runs = append(runs, "run shellcheck -s sh "+path, "run shfmt -ln posix -d -i 4 "+path)
	}
	return append(runs, "go run ./tools/sizecheck "+path,
		"run gitleaks dir --config=.gitleaks.toml --ignore-gitleaks-allow "+
			"--gitleaks-ignore-path=/dev/null --no-banner --redact --verbose "+path,
		"go run ./tools/denycheck "+path)
}

// markdownRuns is the runs that make lint-file makes on a.md.
var markdownRuns = lintFile("a.md", "valecheck a.md",
	"run lychee --offline --include-fragments --no-progress a.md",
	"run rumdl check --no-cache --deny-config-warnings a.md")

// lintFileTests are files of each type, the runs that make lint-file makes on each, and the
// pattern of a run that fails. Where one fails, lint-file runs the rest, then fails.
var lintFileTests = []struct {
	file, fail string
	runs       []string
}{
	{file: "b/c.go", runs: lintFile("b/c.go", "gofmt -l b/c.go",
		"run golangci-lint run --allow-serial-runners ./b/", "valecheck b/c.go")},
	{file: "a.md", runs: markdownRuns},
	{file: "a.md", fail: "run lychee *", runs: markdownRuns},
	{file: ".github/workflows/ci.yml", runs: lintFile(".github/workflows/ci.yml",
		"run -path shellcheck", "run actionlint -shellcheck= -pyflakes= .github/workflows/ci.yml",
		"run zizmor --strict-collection --offline .github/workflows/ci.yml")},
	{file: ".golangci.yml", runs: lintFile(".golangci.yml", "run golangci-lint config verify")},
	{file: "hook.sh", runs: lintFile("hook.sh")},
	{file: "notes.txt", runs: lintFile("notes.txt")},
	{file: "notes.txt", fail: "go run ./tools/denycheck *", runs: lintFile("notes.txt")},
}

// TestLintFile runs make lint-file on a file of each type, as the lint plugin runs it after an
// edit. The directory's go, gofmt, tools/run and tools/valecheck are fakes.
func TestLintFile(t *testing.T) {
	t.Parallel()
	dir, fakes := t.TempDir(), t.TempDir()
	write(t, dir, map[string]string{"b/c.go": "package b\n", "a.md": "# A\n",
		"hook.sh": "#!/bin/sh\n", ".github/workflows/ci.yml": "name: ci\n",
		".golangci.yml": "version: \"2\"\n", "notes.txt": "A\n", "tools/run": fakeGate,
		"tools/valecheck": fakeGate})
	write(t, fakes, map[string]string{"go": fakeGate, "gofmt": fakeGate})
	for _, path := range []string{filepath.Join(dir, "tools", "run"),
		filepath.Join(dir, "tools", "valecheck"), filepath.Join(fakes, "go"),
		filepath.Join(fakes, "gofmt")} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// lint-file runs make again for some gates, which reads the Makefile in dir.
	err := os.Symlink(filepath.Join(root(t), "Makefile"), filepath.Join(dir, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range lintFileTests {
		log := filepath.Join(t.TempDir(), "log")
		env := append(environ(t), "PATH="+fakes+string(os.PathListSeparator)+os.Getenv("PATH"),
			"FAKE_LOG="+log, "FAKE_FAIL="+tt.fail)
		r := command(t, dir, env, "make", "-s", "lint-file", "FILES="+tt.file)
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if (r.status != 0) != (tt.fail != "") || !slices.Equal(got, tt.runs) {
			t.Errorf("make lint-file FILES=%s with %q failing = %d, stderr %q, and the fakes ran"+
				"\n%q\nwant it to fail %v, and\n%q", tt.file, tt.fail, r.status, r.stderr, got,
				tt.fail != "", tt.runs)
		}
	}
}
