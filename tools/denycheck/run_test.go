package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// check runs denycheck in dir with args, the list and stdin, and returns its exit status and its
// findings.
func check(t *testing.T, dir string, stdin string, args ...string) (int, []string) {
	t.Helper()
	var stdout, stderr strings.Builder
	status := run(dir, args, withList, strings.NewReader(stdin), &stdout, &stderr)
	if status == 2 {
		t.Fatalf("denycheck %s: %s", strings.Join(args, " "), stderr.String())
	}
	return status, strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
}

// TestRun checks the files git lists, untracked ones too, and their paths. It passes over a file
// git ignores, one gone from disk and a nested repository, and reads a symbolic link's target.
func TestRun(t *testing.T) {
	t.Parallel()
	dir := repository(t, map[string]string{
		".gitignore": "/ignored/\n", "clean.go": "package a\n", "gone.md": "Contoso\n",
		"README.md": "# Widgets\n\nMade for Acme Widgets.\n", "docs/contoso-notes.md": "# Notes\n",
		"bin/blob": "\x00\x01acme_widgets\x00\xff",
	})
	write(t, dir, map[string]string{"new.txt": "a\n198.51.100.7\n", "ignored/a.md": "Contoso\n",
		"nested/a.md": "Contoso\n"})
	git(t, filepath.Join(dir, "nested"), "init", "-q")
	if err := os.Symlink("../contoso", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	status, got := check(t, dir, "")
	want := []string{"README.md:3: term 1", "bin/blob:1: term 1",
		"docs/[term 4]-notes.md: the path holds term 4", "link:1: term 4", "new.txt:2: term 5"}
	if status != 1 || !slices.Equal(got, want) {
		t.Errorf("status %d, findings\n%q\nwant 1 and\n%q", status, got, want)
	}
	status, got = check(t, dir, "", "new.txt", filepath.Join(dir, "clean.go"), "../a", "ignored/a.md")
	if want := []string{"new.txt:2: term 5"}; status != 1 || !slices.Equal(got, want) {
		t.Errorf("with FILEs: status %d, findings %q; want 1 and %q", status, got, want)
	}
	if status, got := check(t, dir, "", "clean.go"); status != 0 || got[0] != "" {
		t.Errorf("a clean FILE: status %d, findings %q; want 0 and none", status, got)
	}
}

// TestRunHistory checks the objects of a history: a commit's message and author, and a tag. A
// file that a later commit takes out is found, and so is a name a commit gives the same file.
func TestRunHistory(t *testing.T) {
	t.Parallel()
	dir := repository(t, map[string]string{"a.md": "Made for Acme Widgets.\n", "b.md": "fine\n"})
	git(t, dir, "commit", "-q", "-m", "feat: a")
	git(t, dir, "rm", "-q", "a.md")
	git(t, dir, "commit", "-q", "--author", "Ann <ann@contoso.example>", "-m",
		"fix: b\n\n- 198.51.100.7 goes")
	git(t, dir, "mv", "b.md", "fabrikam-labs.md")
	git(t, dir, "commit", "-q", "-m", "fix: c")
	git(t, dir, "tag", "-a", "-m", "for Contoso", "v1")
	id := func(rev string) string { return git(t, dir, "rev-parse", rev)[:12] }
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")[:12]
	blob := git(t, dir, "rev-parse", "HEAD~2:a.md")[:12]
	status, got := check(t, dir, "", "-history", "--all")
	slices.Sort(got)
	want := []string{"a.md@" + blob + ":1: term 1", "commit " + id("HEAD~1") + ":3: term 4",
		"commit " + id("HEAD~1") + ":8: term 5", "tag " + id("v1") + ":6: term 4",
		"tree " + tree + ":1: term 6"}
	if status != 1 || !slices.Equal(got, want) {
		t.Errorf("status %d, findings\n%q\nwant 1 and\n%q", status, got, want)
	}
	status, got = check(t, dir, "", "-history", "HEAD~1..HEAD")
	if want := []string{"tree " + tree + ":1: term 6"}; status != 1 || !slices.Equal(got, want) {
		t.Errorf("HEAD~1..HEAD: status %d, findings %q; want 1 and %q", status, got, want)
	}
	if status, got := check(t, dir, "", "-history", "HEAD..HEAD"); status != 0 || got[0] != "" {
		t.Errorf("no commit: status %d, findings %q; want 0 and none", status, got)
	}
}

// TestRunFiles checks files as they are, outside a repository, and the input.
func TestRunFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, map[string]string{"contoso-1": "\x7fELF\x00acme widgets", "b": "fine\n"})
	status, got := check(t, dir, "", "-files", filepath.Join(dir, "contoso-1"),
		filepath.Join(dir, "b"))
	masked := filepath.Join(dir, "[term 4]-1")
	want := []string{masked + ": the path holds term 4", masked + ":1: term 1"}
	if status != 1 || !slices.Equal(got, want) {
		t.Errorf("status %d, findings %q; want 1 and %q", status, got, want)
	}
	status, got = check(t, dir, "Fix the CONTOSO link\nfine\nfabrikam labs\n", "-stdin", "body")
	if want := []string{"body:1: term 4", "body:3: term 6"}; status != 1 || !slices.Equal(got, want) {
		t.Errorf("-stdin: status %d, findings %q; want 1 and %q", status, got, want)
	}
}

// TestRunNoList checks that denycheck passes with a note where it finds no list, and fails where
// one is required.
func TestRunNoList(t *testing.T) {
	t.Parallel()
	env := environ(map[string]string{"HOME": t.TempDir()})
	for args, want := range map[string]int{"-stdin body": 0, "-require -stdin body": 2} {
		var stdout, stderr strings.Builder
		status := run(t.TempDir(), strings.Fields(args), env, strings.NewReader("Contoso\n"),
			&stdout, &stderr)
		if status != want || stdout.Len() != 0 || !strings.Contains(stderr.String(), "no list") {
			t.Errorf("%s: status %d, stdout %q, stderr %q; want %d and a note", args, status,
				stdout.String(), stderr.String(), want)
		}
	}
}
