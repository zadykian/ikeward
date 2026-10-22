package install

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// context is how many unchanged lines a hunk shows around a change, as diff -u does.
const context = 3

// edit is a line of a diff: kept (' '), removed ('-') or added ('+').
type edit struct {
	kind byte
	line string
}

// unified is a unified diff of old, the file named from, and new, the file named to, as diff -u
// prints it but for the files' times.
func unified(from, to string, old, new []byte) string {
	edits := diff(lines(old), lines(new))
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", from, to)
	// oldLine and newLine count the lines of each file before edits[i].
	oldLine, newLine := 0, 0
	for i := 0; i < len(edits); {
		if edits[i].kind == ' ' {
			oldLine, newLine = oldLine+1, newLine+1
			i++
			continue
		}
		// The hunk starts with the kept lines before its first change, which are counted.
		start, end := max(0, i-context), hunkEnd(edits, i)
		oldStart, newStart := oldLine-(i-start), newLine-(i-start)
		oldCount, newCount := 0, 0
		for _, e := range edits[start:end] {
			if e.kind != '+' {
				oldCount++
			}
			if e.kind != '-' {
				newCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", span(oldStart, oldCount), span(newStart, newCount))
		for _, e := range edits[start:end] {
			out.WriteString(string(e.kind) + e.line)
			if !strings.HasSuffix(e.line, "\n") {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
		oldLine, newLine = oldStart+oldCount, newStart+newCount
		i = end
	}
	return out.String()
}

// hunkEnd is the end of the hunk whose first change is edits[i]. Changes join it while fewer than
// twice context kept lines part them, and context kept lines follow the last.
func hunkEnd(edits []edit, i int) int {
	for {
		for i < len(edits) && edits[i].kind != ' ' {
			i++
		}
		kept := i
		for kept < len(edits) && edits[kept].kind == ' ' {
			kept++
		}
		if kept == len(edits) || kept-i > 2*context {
			return min(i+context, len(edits))
		}
		i = kept
	}
}

// span is a hunk's range of lines of one file: the first, after count lines before it, and the
// count. diff -u leaves the count out where it equals 1. An empty range starts at the line before
// it.
func span(before, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", before)
	case 1:
		return fmt.Sprintf("%d", before+1)
	}
	return fmt.Sprintf("%d,%d", before+1, count)
}

// lines splits data into lines, each with its newline but a last that lacks one.
func lines(data []byte) []string {
	var out []string
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n') + 1
		if i == 0 {
			i = len(data)
		}
		out = append(out, string(data[:i]))
		data = data[i:]
	}
	return out
}

// diff returns the edits that turn a into b, keeping a longest run of lines common to both. Where
// a change removes and adds, the removal comes first, as diff -u prints them. The files are short,
// so the table of common lengths costs little.
func diff(a, b []string) []edit {
	// common[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	common := make([][]int, len(a)+1)
	for i := range common {
		common[i] = make([]int, len(b)+1)
	}
	for i, x := range slices.Backward(a) {
		for j, y := range slices.Backward(b) {
			if x == y {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}
	var edits []edit
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			edits = append(edits, edit{' ', a[i]})
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || common[i+1][j] >= common[i][j+1]):
			edits = append(edits, edit{'-', a[i]})
			i++
		default:
			edits = append(edits, edit{'+', b[j]})
			j++
		}
	}
	return edits
}
