package main

import (
	"slices"
	"strings"
	"testing"
)

// mirrorTests are Go sources and the lines of their mirrors.
var mirrorTests = []struct {
	name string
	src  string
	want []string
}{
	{
		name: "code is blank",
		src:  "package a\n\nvar s = \"// no comment\"\n",
		want: []string{"", "", ""},
	},
	{
		name: "a doc comment's name is masked, and its words stay",
		src:  "package a\n\n// Run validates it.\n// It is done.\nfunc Run() {}\n",
		want: []string{"", "", "xxx validates it.", "It is done.", ""},
	},
	{
		name: "the name after an article, and the package's",
		src:  "// Package a holds T.\npackage a\n\n// A T is a type.\ntype T int\n",
		want: []string{"Package x holds T.", "", "", "A x is a type.", ""},
	},
	{
		name: "a name that only starts the first word stays",
		src:  "package a\n\n// Runs are many.\nfunc Run() {}\n",
		want: []string{"", "", "Runs are many.", ""},
	},
	{
		name: "trailing comments on lines in a row are blocks apart",
		src:  "package a\n\nconst (\n\tb = 1 // one\n\tc = 2 // two\n)\n",
		want: []string{"", "", "", "one", "- two", ""},
	},
	{
		name: "a comment after a trailing one is a block apart",
		src:  "package a\n\nvar b = 1 // one\n// Two.\nvar c = 2\n",
		want: []string{"", "", "one", "- Two.", ""},
	},
	{
		name: "directives are blank",
		src: "//go:build linux\n\npackage a\n\nfunc f() {\n\t_ = 1 //nolint:errcheck // why\n" +
			"\t//nolint:gosec // why\n}\n",
		want: []string{"", "", "", "", "", "", "", ""},
	},
	{
		name: "a code block in a comment is blank",
		src:  "package a\n\n// Use it so:\n//\n//\tgo run .\n//\n// Done.\nvar b int\n",
		want: []string{"", "", "Use it so:", "", "", "", "Done.", ""},
	},
	{
		name: "a list's items and the lines that continue them",
		src: "package a\n\n// Kinds:\n//   - one, which\n//     goes on\n//   • two\n//  2. three\n" +
			"var b int\n",
		want: []string{"", "", "Kinds:", "- one, which", "goes on", "- two", "- 2. three", ""},
	},
	{
		name: "a paragraph right after a list starts as an item",
		src:  "package a\n\nfunc f() {\n\t// Kinds:\n\t//   - one\n\t// Then more.\n}\n",
		want: []string{"", "", "", "Kinds:", "- one", "- Then more.", ""},
	},
	{
		name: "a line that would start a block goes escaped, and a fence goes as '",
		src: "package a\n\n// one\n// - two\n// 3. three\n// # four\n// ```\n// `five` six\n" +
			"var b int\n",
		want: []string{"", "", "one", `\- two`, `\3. three`, `\# four`, "'''", "`five` six", ""},
	},
	{
		name: "a dollar sign goes as another sign of one rune",
		src:  "package a\n\n// It reads $A, then\n// stops ($).\nvar b int\n",
		want: []string{"", "", "It reads ¤A, then", "stops (¤).", ""},
	},
	{
		name: "a < goes as another sign of one rune, so that it opens no tag or HTML comment",
		src: "package a\n\nfunc f() {\n\t// <b> wraps <pre> and <!-- vale off --> in x<y>z.\n" +
			"\t// Then a<b.\n}\n",
		want: []string{"", "", "", "‹b> wraps ‹pre> and ‹!-- vale off --> in x‹y>z.", "Then a‹b.", ""},
	},
	{
		name: "a backtick that opens no code span on its line goes as ', and a span stays one",
		src: "package a\n\n// It's `a lone tick\n// then `code` here, `` `and` `` ```.\n" +
			"var b int\n",
		want: []string{"", "", "It's 'a lone tick", "then `code` here, `` `and` `` '''.", ""},
	},
	{
		name: "Go's ``quote'' goes as ''quote''",
		src:  "package a\n\n// Say ``yes'' and ``no''.\nvar b int\n",
		want: []string{"", "", "Say ''yes'' and ''no''.", ""},
	},
	{
		name: "a block comment loses its markers and the indentation its lines share",
		src:  "package a\n\nfunc f() {\n\t/* one\n\t   two\n\t   three */\n}\n",
		want: []string{"", "", "", "one", "two", "three", ""},
	},
	{
		name: "two comments on a line",
		src:  "package a\n\nvar b = /* one */ 1 // two\n",
		want: []string{"", "", "one two"},
	},
	{
		name: "a file that fails to parse, without a final newline",
		src:  "package a\n\nfunc {\n// The rest.",
		want: []string{"", "", "", "The rest."},
	},
}

func TestMirror(t *testing.T) {
	t.Parallel()
	for _, tt := range mirrorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, r := range mirror([]byte(tt.src)) {
				got = append(got, r.text)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("mirror(%q) =\n%q\nwant\n%q", tt.src, got, tt.want)
			}
		})
	}
}

// columnTests are Go sources, each with a word in a comment. The pieces of the word's line must
// map its column in the mirror to its column in the source.
var columnTests = []struct {
	name, src, word string
}{
	{name: "after code", src: "package a\n\nvar b = 1 // the word\n", word: "word"},
	{name: "after runes of two bytes", src: "package a\n\nvar é = 1 // é word\n", word: "word"},
	{name: "in a list", src: "package a\n\n// List:\n//   - the word\nvar b int\n", word: "word"},
	{
		name: "after an escape", src: "package a\n\n// one\n// - the word\nvar b int\n",
		word: "word",
	},
	{
		name: "after a block's start",
		src:  "package a\n\nvar b = 1 // one\n\t// the word\nvar c int\n", word: "word",
	},
	{name: "a second comment", src: "package a\n\nvar b = /* a */ 1 // the word\n", word: "word"},
}

func TestColumns(t *testing.T) {
	t.Parallel()
	for _, tt := range columnTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := strings.Split(tt.src, "\n")
			for i, r := range mirror([]byte(tt.src)) {
				at := strings.Index(r.text, tt.word)
				if at < 0 {
					continue
				}
				got := sourceColumn(r.pieces, runes(r.text[:at])+1)
				want := runes(lines[i][:strings.LastIndex(lines[i], tt.word)]) + 1
				if got != want {
					t.Errorf("line %d: %q at column %d, want %d", i+1, tt.word, got, want)
				}
				return
			}
			t.Errorf("no %q in the mirror of %q", tt.word, tt.src)
		})
	}
}

// runes returns the number of runes in s.
func runes(s string) int {
	return len([]rune(s))
}

// listTests are lines of comments, and whether each starts with a list marker.
var listTests = []struct {
	line string
	want bool
}{
	{"  - item", true},
	{"  * item", true},
	{"  + item", true},
	{"  • item", true},
	{"  1. item", true},
	{"  12) item", true},
	{"  -item", false},
	{"  - ", false},
	{"  1.5 item", false},
	{"  1", false},
	{"  item", false},
	{"", false},
}

func TestList(t *testing.T) {
	t.Parallel()
	for _, tt := range listTests {
		if got := list(tt.line); got != tt.want {
			t.Errorf("list(%q) = %t, want %t", tt.line, got, tt.want)
		}
	}
}
