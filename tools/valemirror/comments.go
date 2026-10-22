package main

import (
	"go/scanner"
	"go/token"
	"strings"
	"unicode/utf8"
)

// A text is what a comment says on one line of the source, its markers left out.
type text struct {
	line, col int // where the text starts: the line and the rune's column, from 1
	s         string
	directive bool // a directive such as //go:build or //nolint, which is not prose
}

// A group is a comment group as go/parser groups comments, a text per line in order.
type group []text

// groups returns the comment groups of the Go source src. It reads tokens alone, so that a file
// that fails to parse still has its comments read.
func groups(src []byte) []group {
	file := token.NewFileSet().AddFile("", -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, scanner.ScanComments)
	var all []group
	open, first := false, true
	prev, end, near := 0, 0, 0
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return all
		}
		line := file.Line(pos)
		if tok != token.COMMENT {
			open, first, prev = false, true, line
			continue
		}
		// As go/parser: a comment on its token's line takes only comments on that line.
		if !open || line > end+near {
			near = 1
			if first && line == prev {
				near = 0
			}
			all = append(all, nil)
			open = true
		}
		first = false
		all[len(all)-1] = append(all[len(all)-1], texts(src, file.Offset(pos), line, lit)...)
		end = line + strings.Count(lit, "\n")
	}
}

// texts returns the lines of the comment lit, which starts at offset on line of src.
func texts(src []byte, offset, line int, lit string) []text {
	col := column(src, offset) + 2
	if body, ok := strings.CutPrefix(lit, "//"); ok {
		t := text{line: line, col: col, s: body, directive: directive(body)}
		if rest, ok := strings.CutPrefix(body, " "); ok {
			t.s, t.col = rest, col+1
		}
		return []text{t}
	}
	body := strings.TrimSuffix(strings.TrimPrefix(lit, "/*"), "*/")
	parts := strings.Split(body, "\n")
	all := []text{{line: line, col: col, s: parts[0]}}
	if rest, ok := strings.CutPrefix(parts[0], " "); ok {
		all[0].s, all[0].col = rest, col+1
	}
	// The lines after the first lose the indentation they share, the code's.
	cut := indentation(parts[1:])
	for i, part := range parts[1:] {
		n := min(cut, len(part))
		all = append(all, text{line: line + 1 + i, col: 1 + n, s: part[n:]})
	}
	return all
}

// indentation returns the number of blanks and tabs that the lines with text all start with.
func indentation(lines []string) int {
	cut := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if cut < 0 || n < cut {
			cut = n
		}
	}
	return max(cut, 0)
}

// column returns the column, counted in runes from 1, of the byte at offset in src.
func column(src []byte, offset int) int {
	start := offset
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	return utf8.RuneCount(src[start:offset]) + 1
}

// directive returns whether the text of a // comment, its // left out, is a directive: go/ast's
// rule, and //nolint, which golangci-lint reads.
func directive(body string) bool {
	for _, prefix := range []string{"line ", "extern ", "export ", "nolint"} {
		if strings.HasPrefix(body, prefix) {
			return true
		}
	}
	colon := strings.Index(body, ":")
	if colon <= 0 || colon+1 >= len(body) {
		return false
	}
	for i := 0; i <= colon+1; i++ {
		b := body[i]
		if i != colon && (b < 'a' || b > 'z') && (b < '0' || b > '9') {
			return false
		}
	}
	return true
}
