package main

import (
	"bufio"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// A source is the Go file a mirror stands for, and the pieces of each line that has prose.
type source struct {
	File  string          `json:"file"`
	Lines map[int][]piece `json:"lines"`
}

// indexName is the file in a mirrors' directory that maps each mirror, by its path there, to its
// source.
const indexName = "index.json"

// translate copies Vale's alerts, in its line output, from in to out. An alert in a mirror under
// dir names the Go file and the line and column there instead.
func translate(dir string, index map[string]source, in io.Reader, out io.Writer) error {
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	lines := bufio.NewScanner(in)
	lines.Buffer(nil, 1<<20)
	w := bufio.NewWriter(out)
	for lines.Scan() {
		alert := lines.Text()
		if rest, ok := strings.CutPrefix(alert, prefix); ok {
			alert = locate(index, filepath.ToSlash(rest), alert)
		}
		if _, err := w.WriteString(alert + "\n"); err != nil {
			return err
		}
	}
	if err := lines.Err(); err != nil {
		return err
	}
	return w.Flush()
}

// locate returns the alert, whose path is mirror:LINE:COL:..., with the source's path and column,
// or unchanged where the index has no such mirror.
func locate(index map[string]source, rest, alert string) string {
	name, tail, ok := strings.Cut(rest, ".md:")
	src, known := index[name+".md"]
	if !ok || !known {
		return alert
	}
	field := strings.SplitN(tail, ":", 3)
	if len(field) < 3 {
		return alert
	}
	line, err := strconv.Atoi(field[0])
	if err != nil {
		return alert
	}
	col, err := strconv.Atoi(field[1])
	if err != nil {
		return alert
	}
	col = sourceColumn(src.Lines[line], col)
	return src.File + ":" + field[0] + ":" + strconv.Itoa(col) + ":" + field[2]
}

// sourceColumn returns the column in the source of the column col of a mirror's line made of
// pieces. A column before the first piece, in what the mirror added, goes to its start.
func sourceColumn(pieces []piece, col int) int {
	if len(pieces) == 0 {
		return col
	}
	p := pieces[0]
	for _, next := range pieces[1:] {
		if next.Mirror > col {
			break
		}
		p = next
	}
	return p.Source + max(col-p.Mirror, 0)
}
