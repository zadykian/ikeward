package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// switchText matches the text of an HTML comment that turns a gate off or on: Vale's, rumdl's or
// markdownlint's, as in `<!-- vale off -->` or `<!-- rumdl-disable MD013 -->`.
var switchText = regexp.MustCompile(`(?i)^\s*(vale\b|rumdl-|markdownlint-)`)

// switchAlert is the rule and the message of an alert on such a comment, as Vale writes them.
const switchAlert = "valemirror.Switch:A comment turns a gate off; rewrite the text instead."

// A position is where a comment starts: the line, and the column of its <!-- in runes, from 1.
type position struct{ line, col int }

// printSwitches prints to out, as Vale's alerts, the comments in the Go or Markdown files that
// turn a gate off, outside code. A file gone from disk has none.
func printSwitches(files []string, out io.Writer) error {
	w := bufio.NewWriter(out)
	for _, file := range files {
		src, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, p := range fileSwitches(file, src) {
			if _, err := fmt.Fprintf(w, "%s:%d:%d:%s\n", file, p.line, p.col,
				switchAlert); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}

// fileSwitches returns where the file holds a comment that turns a gate off. A Markdown file's
// fenced code blocks hold none; a Go file's are in the prose of its comments, a group at a time.
func fileSwitches(file string, src []byte) []position {
	if markdownFile(file) {
		var lines []text
		for i, s := range strings.Split(string(src), "\n") {
			lines = append(lines, text{line: i + 1, col: 1, s: s})
		}
		return switches(lines, fences())
	}
	var found []position
	for _, g := range groups(src) {
		var lines []text
		for _, it := range prose(g) {
			lines = append(lines, it.text)
		}
		found = append(found, switches(lines, nil)...)
	}
	return found
}

// switches returns where the lines hold a comment that turns a gate off, outside code spans and
// the lines that code, if not nil, reports as code. A comment runs from its <!-- to the next -->,
// on a later line too, and is read whole: none of its lines is code.
func switches(lines []text, code func(string) bool) []position {
	var found []position
	for i := 0; i < len(lines); i++ {
		if code != nil && code(lines[i].s) {
			continue
		}
		for from := 0; ; {
			at := opening(lines[i].s[from:])
			if at < 0 {
				break
			}
			at += from
			body, j, end := closing(lines, i, at+len("<!--"))
			if switchText.MatchString(body) {
				col := lines[i].col + utf8.RuneCountInString(lines[i].s[:at])
				found = append(found, position{line: lines[i].line, col: col})
			}
			if j == len(lines) {
				return found
			}
			i, from = j, end
		}
	}
	return found
}

// opening returns the byte offset of the first <!-- in a line of prose outside its code spans,
// or -1 for a line with none.
func opening(s string) int {
	code, _ := spans(s)
	for from := 0; ; {
		at := strings.Index(s[from:], "<!--")
		if at < 0 {
			return -1
		}
		if at += from; !within(code, at) {
			return at
		}
		from = at + 1
	}
}

// closing returns the text of a comment from the byte start of lines[i] to its -->, the index of
// the line with the -->, and the byte offset past it there. With no -->, the comment runs to the
// end, and the index is len(lines).
func closing(lines []text, i, start int) (string, int, int) {
	var body strings.Builder
	for j := i; j < len(lines); j++ {
		s := lines[j].s[start:]
		if k := strings.Index(s, "-->"); k >= 0 {
			body.WriteString(s[:k])
			return body.String(), j, start + k + len("-->")
		}
		body.WriteString(s + "\n")
		start = 0
	}
	return body.String(), len(lines), 0
}

// fences returns a func that reads the lines of a Markdown file in order, and reports whether
// each is a fence or in a fenced code block.
func fences() func(string) bool {
	fence := ""
	return func(line string) bool {
		rest := strings.TrimLeft(line, " ")
		indented := len(line)-len(rest) > 3
		switch {
		case fence != "":
			after := strings.TrimLeft(rest, fence[:1])
			if !indented && strings.HasPrefix(rest, fence) && strings.TrimSpace(after) == "" {
				fence = ""
			}
		case !indented && opensFence(rest) != "":
			fence = opensFence(rest)
		default:
			return false
		}
		return true
	}
}

// opensFence returns the fence that a line of Markdown, its indentation cut, opens: three or more
// backticks or tildes, and no backtick after backticks. It returns "" for a line that opens none.
func opensFence(line string) string {
	for _, mark := range "`~" {
		fence := line[:len(line)-len(strings.TrimLeft(line, string(mark)))]
		if len(fence) >= 3 && (mark != '`' || !strings.Contains(line[len(fence):], "`")) {
			return fence
		}
	}
	return ""
}
