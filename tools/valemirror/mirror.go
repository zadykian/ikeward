package main

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// A piece is a run of a mirror's line copied from the source: the columns, from 1 in runes, where
// it starts in each.
type piece struct {
	Mirror int `json:"mirror"`
	Source int `json:"source"`
}

// A row is a line of a mirror: its Markdown, and where its runs of text come from.
type row struct {
	text   string
	pieces []piece
}

// An item is a line of prose. start marks the first line of a paragraph or of a list item, and
// escape a line whose first character Markdown must not read as the start of a block.
type item struct {
	text
	start, escape bool
}

// mirror returns the Markdown mirror of the Go source src, a row per line. Rows hold the prose of
// comments; code, directives and the code blocks of comments are blank. Doc comments' declared
// names are masked.
func mirror(src []byte) []row {
	n := bytes.Count(src, []byte("\n"))
	if len(src) > 0 && src[len(src)-1] != '\n' {
		n++
	}
	rows := make([]row, n)
	names := docNames(src)
	for _, g := range groups(src) {
		items := prose(g)
		if len(items) > 0 && len(names[g[0].line]) > 0 {
			items[0].s = mask(items[0].s, names[g[0].line])
		}
		for _, it := range items {
			place(rows, it)
		}
	}
	return rows
}

// place adds the item to its row. A block right below prose goes as a list item, since Markdown
// would run a paragraph on from the prose above.
func place(rows []row, it item) {
	r := &rows[it.line-1]
	prefix := ""
	switch {
	case r.text != "":
		prefix = " "
	case it.start && it.line > 1 && rows[it.line-2].text != "" && (it.escape || !bullet(it.s)):
		prefix = "- "
	}
	if it.escape {
		prefix += `\`
	}
	at := utf8.RuneCountInString(r.text) + utf8.RuneCountInString(prefix) + 1
	r.text += prefix + plain(it.s)
	r.pieces = append(r.pieces, piece{Mirror: at, Source: it.col})
}

// dollar stands for $ in a mirror, as Vale skips the text between two $ of a paragraph, and angle
// for <, which would start HTML. Each is one rune, so the columns hold, and Vale counts each as it
// counts the sign (docs/checks.md).
const (
	dollar = "¤"
	angle  = "‹"
)

// plain returns a line of a comment's prose as Markdown that reads as the comment does, rune for
// rune. $ and < go as stand-ins, and each backtick of a run that opens no code span on the line
// as '. A code span on the line stays one.
func plain(s string) string {
	_, loose := spans(s)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '$':
			b.WriteString(dollar)
		case s[i] == '<':
			b.WriteString(angle)
		case s[i] == '`' && within(loose, i):
			b.WriteByte('\'')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// prose returns the lines of prose in a comment group, read as go/doc reads a doc comment. An
// indented span is a list where it starts with a list marker, and code otherwise.
func prose(g group) []item {
	lines := make([]string, len(g))
	for i, t := range g {
		if !t.directive {
			lines[i] = strings.TrimRight(t.s, " \t")
		}
	}
	var items []item
	for i := 0; i < len(lines); {
		start := i
		switch {
		case lines[i] == "":
			i++
		case indented(lines[i]):
			for i < len(lines) && (lines[i] == "" || indented(lines[i])) {
				i++
			}
			if list(lines[start]) {
				items = append(items, listItems(g[start:i], lines[start:i])...)
			}
		default:
			for ; i < len(lines) && lines[i] != "" && !indented(lines[i]); i++ {
				t := trim(g[i], lines[i])
				items = append(items, item{text: t, start: i == start, escape: escapes(t.s)})
			}
		}
	}
	return items
}

// listItems returns the prose of a list's lines: each marker starts an item, and the lines
// between continue it.
func listItems(g group, lines []string) []item {
	var items []item
	for i, line := range lines {
		if line == "" {
			continue
		}
		t := trim(g[i], line)
		if !list(line) {
			items = append(items, item{text: t, escape: escapes(t.s)})
			continue
		}
		// Markdown has no bullet •, and its - is as wide.
		if rest, ok := strings.CutPrefix(t.s, "•"); ok {
			t.s = "-" + rest
		}
		items = append(items, item{text: t, start: true})
	}
	return items
}

// trim returns t with the text s, its leading blanks and tabs cut and its column moved past them.
func trim(t text, s string) text {
	trimmed := strings.TrimLeft(s, " \t")
	t.col += len(s) - len(trimmed)
	t.s = trimmed
	return t
}

// indented returns whether a comment's line is indented, as go/doc/comment reads it.
func indented(line string) bool {
	return line != "" && (line[0] == ' ' || line[0] == '\t')
}

// list returns whether a comment's line starts with a list marker, as go/doc/comment reads it.
func list(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	rest := ""
	if r, n := utf8.DecodeRuneInString(line); strings.ContainsRune("•*+-", r) {
		rest = line[n:]
	} else {
		digits := len(line) - len(strings.TrimLeft(line, "0123456789"))
		if digits == 0 || digits == len(line) || (line[digits] != '.' && line[digits] != ')') {
			return false
		}
		rest = line[digits+1:]
	}
	return indented(rest) && strings.TrimSpace(rest) != ""
}

// bullet returns whether a line of Markdown starts a bullet list item.
func bullet(line string) bool {
	return len(line) > 1 && strings.ContainsRune("*+-", rune(line[0])) &&
		(line[1] == ' ' || line[1] == '\t')
}

// escapes returns whether a line of prose starts with a character that could begin a Markdown
// block, a list, heading, quote, fence, table or rule, and must go escaped. A < goes as angle
// instead, and the backticks of a fence, which pair with none on the line, as ' (plain).
func escapes(s string) bool {
	return s != "" && strings.ContainsRune("-+*#>=_~|0123456789", rune(s[0]))
}
