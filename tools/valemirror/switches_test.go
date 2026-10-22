package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// switchSources are files by their names, each with comments that turn a gate off, on one line
// or across lines, and that quote one.
var switchSources = map[string]string{
	"a.md": "# A\n\n<!-- vale off -->\nQuoted: `<!-- vale off -->`.\n\n````md\n```\n" +
		"<!-- vale on -->\n````\n<!-- rumdl-disable MD013 -->\n\n~~~~\n<!-- vale x = NO -->\n~~~~\n" +
		"é <!--markdownlint-disable-next-line--> and <!-- VALE Microsoft.SentenceLength = NO -->\n" +
		"    <!-- rumdl-enable -->\n<!-- a comment -->\n",
	"a.go": "package a\n\n// F does it. <!-- vale off -->\n//\n//\t<!-- vale off -->\n" +
		"func F() {}\n\nvar s = \"<!-- vale off -->\" // `<!-- vale off -->`, quoted\n\n" +
		"// G does it. <!--\n// vale off -->\nfunc G() {}\n",
	"b.md": "# B\n\n<!--\nvale off\n-->\nQuoted: `<!--`\nvale off -->\n\n" +
		"<!--\n  vale Microsoft.SentenceLength = NO\n-->\n```\n<!--\n```\nvale off\n-->\n" +
		"<!-- a\n```\n--> <!-- vale on -->\né <!--\nrumdl-disable -->\n",
}

// switchWant is what printSwitches prints for switchSources, $DIR standing for their directory.
const switchWant = "$DIR/a.go:3:15:" + switchAlert + "\n" +
	"$DIR/a.go:10:15:" + switchAlert + "\n" +
	"$DIR/a.md:3:1:" + switchAlert + "\n" +
	"$DIR/a.md:10:1:" + switchAlert + "\n" +
	"$DIR/a.md:15:3:" + switchAlert + "\n" +
	"$DIR/a.md:15:45:" + switchAlert + "\n" +
	"$DIR/a.md:16:5:" + switchAlert + "\n" +
	"$DIR/b.md:3:1:" + switchAlert + "\n" +
	"$DIR/b.md:9:1:" + switchAlert + "\n" +
	"$DIR/b.md:19:5:" + switchAlert + "\n" +
	"$DIR/b.md:20:3:" + switchAlert + "\n"

// TestSwitches checks that the switches in prose are found at their <!--, and those in code
// spans, code blocks, Go code and other comments are not. A fence in a comment opens no block. A
// file gone from disk has none.
func TestSwitches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := []string{filepath.Join(dir, "a.go"), filepath.Join(dir, "a.md"),
		filepath.Join(dir, "b.md"), filepath.Join(dir, "gone.md")}
	for name, src := range switchSources {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr strings.Builder
	status := run(append([]string{"-switches"}, files...), nil, &stdout, &stderr)
	want := strings.ReplaceAll(switchWant, "$DIR", dir)
	if status != 0 || stdout.String() != want {
		t.Errorf("run -switches = %d, stdout\n%s\nstderr %q; want 0 and\n%s", status,
			stdout.String(), stderr.String(), want)
	}
}
