package main

import (
	"slices"
	"testing"
)

// mustParse returns the terms of a list, which parseList must take.
func mustParse(t *testing.T, list string) []term {
	t.Helper()
	terms, err := parseList(list)
	if err != nil {
		t.Fatal(err)
	}
	return terms
}

// TestText checks the findings of a text: one for a term and a line of the text, by line and
// then term. A term that occurs twice on a line is one finding.
func TestText(t *testing.T) {
	t.Parallel()
	text := "Acme Widgets, and acme_widgets for Contoso\n\n" +
		"x contoso\x00198.51.100.7 \xff acme-widgets\nfine\nContosoContoso, contoso"
	var r report
	r.text("body", []byte(text), mustParse(t, list))
	want := []string{"body:1: term 1", "body:1: term 4", "body:3: term 1", "body:3: term 4",
		"body:3: term 5", "body:5: term 4"}
	if !slices.Equal(r.lines, want) {
		t.Errorf("findings\n%q\nwant\n%q", r.lines, want)
	}
}

// maskTests are paths, and what mask makes of each with the terms widgets, acme-widgets and
// Contoso: the path, and the numbers of the terms it holds.
var maskTests = []struct {
	path, masked string
	terms        []int
}{
	{path: "docs/a.md", masked: "docs/a.md"},
	{path: "docs/contoso/Contoso.md", masked: "docs/[term 3]/[term 3].md", terms: []int{3}},
	{path: "acme-widgets.go", masked: "[term 1, 2].go", terms: []int{1, 2}},
	{path: "widgets/acme_widgets", masked: "[term 1]/[term 1, 2]", terms: []int{1, 2}},
}

func TestMask(t *testing.T) {
	t.Parallel()
	terms := mustParse(t, "widgets\nacme-widgets\nContoso")
	for _, tt := range maskTests {
		masked, numbers := mask(tt.path, terms)
		if masked != tt.masked || !slices.Equal(numbers, tt.terms) {
			t.Errorf("mask(%q) = %q, %v; want %q, %v", tt.path, masked, numbers, tt.masked,
				tt.terms)
		}
	}
	var r report
	if got := r.path("docs/contoso.md", terms); got != "docs/[term 3].md" ||
		!slices.Equal(r.lines, []string{"docs/[term 3].md: the path holds term 3"}) {
		t.Errorf("path = %q, findings %q", got, r.lines)
	}
}

// edgeTests are texts with a | where an edge is or is not.
var edgeTests = map[string]bool{
	"|a": true, "a|": true, "a| b": true, "a |b": true, "a|_": true, "a|1": true, "1|a": true,
	"fo|o": false, "12|3": false, "foo|Bar": true, "FO|O": false, "DNS|Name": true, "DN|SName": false,
	"Foo|bar": false, "é|É": true, "a|\xff": true,
}

func TestEdge(t *testing.T) {
	t.Parallel()
	for text, want := range edgeTests {
		i := len(text)
		for j := range len(text) {
			if text[j] == '|' {
				i = j
			}
		}
		data := []byte(text[:i] + text[i+1:])
		if got := edge(data, i); got != want {
			t.Errorf("edge(%q) = %v, want %v", text, got, want)
		}
	}
}
