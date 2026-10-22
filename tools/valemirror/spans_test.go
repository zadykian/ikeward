package main

import (
	"reflect"
	"testing"
)

// spansTests are lines of Markdown, their code spans and the runs of backticks that open none,
// by their byte ranges.
var spansTests = []struct {
	line        string
	code, loose [][2]int
}{
	{line: "no code"},
	{line: "a `b` c", code: [][2]int{{2, 5}}},
	{line: "`a` and ``b`c``", code: [][2]int{{0, 3}, {8, 15}}},
	{line: "a lone ` tick", loose: [][2]int{{7, 8}}},
	{line: "`a` then ` and `b`", code: [][2]int{{0, 3}, {9, 16}}, loose: [][2]int{{17, 18}}},
	{line: "``a`` b ``", code: [][2]int{{0, 5}}, loose: [][2]int{{8, 10}}},
	{line: "say ``yes'' and ``no''", loose: [][2]int{{4, 6}, {16, 18}}},
	{line: "``yes'' or `x`", code: [][2]int{{11, 14}}, loose: [][2]int{{0, 2}}},
	{line: "```", loose: [][2]int{{0, 3}}},
}

func TestSpans(t *testing.T) {
	t.Parallel()
	for _, tt := range spansTests {
		code, loose := spans(tt.line)
		if !reflect.DeepEqual(code, tt.code) || !reflect.DeepEqual(loose, tt.loose) {
			t.Errorf("spans(%q) = %v, %v; want %v, %v", tt.line, code, loose, tt.code, tt.loose)
		}
	}
}
