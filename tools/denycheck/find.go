package main

import (
	"bytes"
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A hit is a place where a term is found: the term's number, the line of the text, and the
// bytes it spans.
type hit struct {
	term, line, start, end int
}

// places returns each place in data where a term is found, as a word, by line and then term.
func places(terms []term, data []byte) []hit {
	var hits []hit
	for _, t := range terms {
		line, counted := 1, 0
		for start := 0; start < len(data); {
			loc := t.re.FindIndex(data[start:])
			if loc == nil {
				break
			}
			s, e := start+loc[0], start+loc[1]
			if !edge(data, s) || !edge(data, e) {
				_, size := utf8.DecodeRune(data[s:])
				start = s + size
				continue
			}
			line += bytes.Count(data[counted:s], []byte("\n"))
			counted, start = s, e
			hits = append(hits, hit{term: t.n, line: line, start: s, end: e})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(a.line, b.line), cmp.Compare(a.term, b.term),
			cmp.Compare(a.start, b.start))
	})
	return hits
}

// edge returns whether a word of data may start or end at i. Each end of data is an edge, and so
// is a sign that is no letter or digit, a letter beside a digit, and the end of an escape. Camel
// case changes word between a lower-case and an upper-case letter, as in fooBar. It changes word
// before the last capital of a run that starts a word too, as in DNSName.
func edge(data []byte, i int) bool {
	if i == 0 || i == len(data) {
		return true
	}
	a, _ := utf8.DecodeLastRune(data[:i])
	b, size := utf8.DecodeRune(data[i:])
	switch {
	case !word(a) || !word(b), unicode.IsDigit(a) != unicode.IsDigit(b):
		return true
	case escape.Match(data[max(0, i-10):i]):
		return true
	case unicode.IsLower(a) && unicode.IsUpper(b):
		return true
	case unicode.IsUpper(a) && unicode.IsUpper(b):
		c, _ := utf8.DecodeRune(data[i+size:])
		return unicode.IsLower(c)
	}
	return false
}

// escape matches a text that ends in an escape of Go's, C's, JSON's or a URL's, as \n, \x0a or
// %2F. A word starts after one, as in a test's DNS name in wire format.
var escape = regexp.MustCompile(`(?:\\[abefnrtv0]|\\x[[:xdigit:]]{2}|\\u[[:xdigit:]]{4}|` +
	`\\U[[:xdigit:]]{8}|\\[0-7]{3}|%[[:xdigit:]]{2})$`)

// word returns whether r is a letter or a digit, which a word is made of.
func word(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// A report gathers denycheck's findings, a line each: where the term is, then its number.
type report struct {
	lines []string
}

// text adds the findings of data, by its name: NAME:LINE: term N, once for each term and line.
func (r *report) text(name string, data []byte, terms []term) {
	last := hit{}
	for _, h := range places(terms, data) {
		if h.line != last.line || h.term != last.term {
			r.lines = append(r.lines, fmt.Sprintf("%s:%d: term %d", name, h.line, h.term))
		}
		last = h
	}
}

// path adds the findings of a path, a line for each term it holds, and returns the path masked.
func (r *report) path(path string, terms []term) string {
	masked, numbers := mask(path, terms)
	for _, n := range numbers {
		r.lines = append(r.lines, fmt.Sprintf("%s: the path holds term %d", masked, n))
	}
	return masked
}

// mask returns path with each place a term is found in it replaced by [term N], so that a
// finding names no term, and the numbers of those terms.
func mask(path string, terms []term) (string, []int) {
	hits := places(terms, []byte(path))
	if len(hits) == 0 {
		return path, nil
	}
	slices.SortFunc(hits, func(a, b hit) int { return cmp.Compare(a.start, b.start) })
	var b strings.Builder
	var all []int
	last := 0
	for i := 0; i < len(hits); {
		start, end := hits[i].start, hits[i].end
		var group []int
		for ; i < len(hits) && hits[i].start < end; i++ {
			end = max(end, hits[i].end)
			group = append(group, hits[i].term)
		}
		slices.Sort(group)
		group = slices.Compact(group)
		var numbers []string
		for _, n := range group {
			numbers = append(numbers, strconv.Itoa(n))
		}
		all = append(all, group...)
		b.WriteString(path[last:start] + "[term " + strings.Join(numbers, ", ") + "]")
		last = end
	}
	b.WriteString(path[last:])
	slices.Sort(all)
	return b.String(), slices.Compact(all)
}
