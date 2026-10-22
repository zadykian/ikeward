package main

import "testing"

// maskTests are lines that start doc comments, the names declared, and the lines masked.
var maskTests = []struct {
	line  string
	names []string
	want  string
}{
	{"validate checks it.", []string{"validate"}, "xxxxxxxx checks it."},
	{"An entry is a line.", []string{"entry"}, "An xxxxx is a line."},
	{"The é is a rune.", []string{"é"}, "The x is a rune."},
	{"b, c and d are set.", []string{"d", "c", "b"}, "x, c and d are set."},
	{"validated, it runs.", []string{"validate"}, "validated, it runs."},
	{"It validates.", []string{"validate"}, "It validates."},
}

func TestMask(t *testing.T) {
	t.Parallel()
	for _, tt := range maskTests {
		if got := mask(tt.line, tt.names); got != tt.want {
			t.Errorf("mask(%q, %q) = %q, want %q", tt.line, tt.names, got, tt.want)
		}
	}
}
