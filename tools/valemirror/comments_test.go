package main

import (
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// directiveTests are the texts of // comments, their // left out, and whether each is a directive.
var directiveTests = []struct {
	body string
	want bool
}{
	{"go:build linux", true},
	{"go:generate stringer", true},
	{"nolint:errcheck // why", true},
	{"line a.go:1", true},
	{"export f", true},
	{"lint:ignore U1000 why", true},
	{" go:build linux", false},
	{"Note: a sentence", false},
	{"a:", false},
	{":a", false},
	{"a sentence", false},
}

func TestDirective(t *testing.T) {
	t.Parallel()
	for _, tt := range directiveTests {
		if got := directive(tt.body); got != tt.want {
			t.Errorf("directive(%q) = %t, want %t", tt.body, got, tt.want)
		}
	}
}

// TestGroupsAsParser checks that the comment groups of mirrorTests' sources start and end on the
// lines that go/parser gives.
func TestGroupsAsParser(t *testing.T) {
	t.Parallel()
	for _, tt := range mirrorTests {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "", tt.src, parser.ParseComments)
		if err != nil {
			continue
		}
		var want, got [][2]int
		for _, g := range file.Comments {
			want = append(want, [2]int{fset.Position(g.Pos()).Line, fset.Position(g.End()).Line})
		}
		for _, g := range groups([]byte(tt.src)) {
			got = append(got, [2]int{g[0].line, g[len(g)-1].line})
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: groups on lines %v, want %v", tt.name, got, want)
		}
	}
}
