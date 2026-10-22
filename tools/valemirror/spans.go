package main

import "strings"

// spans returns the code spans of a line of Markdown, as byte ranges that take in their
// backticks, and the runs of backticks that open none. A run pairs with the next run of its length
// on the line, as Markdown pairs a span's ends, and the two backticks of a Go quote open none.
func spans(s string) (code, loose [][2]int) {
	var runs [][2]int
	for i := 0; i < len(s); i++ {
		if s[i] != '`' {
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		runs = append(runs, [2]int{i, j})
		i = j - 1
	}
	for k := 0; k < len(runs); k++ {
		r := runs[k]
		end := len(s)
		if k+1 < len(runs) {
			end = runs[k+1][0]
		}
		m := k + 1
		for m < len(runs) && runs[m][1]-runs[m][0] != r[1]-r[0] {
			m++
		}
		if m == len(runs) || r[1]-r[0] == 2 && strings.Contains(s[r[1]:end], "''") {
			loose = append(loose, r)
			continue
		}
		code = append(code, [2]int{r[0], runs[m][1]})
		k = m
	}
	return code, loose
}

// within returns whether the byte offset i is in one of the ranges.
func within(ranges [][2]int, i int) bool {
	for _, r := range ranges {
		if r[0] <= i && i < r[1] {
			return true
		}
	}
	return false
}
