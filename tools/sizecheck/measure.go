package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// The caps: the lines of a file and of the README at the root, of a Go comment block, and of a Go
// package comment.
const (
	maxLines   = 300
	maxReadme  = 150
	maxComment = 4
	maxPackage = 15
)

// A block is a comment block: the line it starts on and its length in lines.
type block struct {
	line, length int
}

// A measurement is what sizecheck finds in a file.
type measurement struct {
	lines  int
	blocks []block // the comment blocks over maxComment, the package comment aside
	pkg    block   // the package comment, of length 0 where there is none
	// Where a Go file fails to parse, and why; errMsg is empty where it parses.
	errLine int
	errMsg  string
}

// A finding is a line of sizecheck's report, PATH:LINE: msg.
type finding struct {
	path string
	line int
	msg  string
}

// The names and first lines by which sizecheck tells a shell script, the regular expressions of
// shfmt -f. Unlike shfmt -f, it reads the first line of every file, hidden or of another extension.
var (
	shellName    = regexp.MustCompile(`\.(sh|bash|mksh|bats|zsh)$`)
	shellShebang = regexp.MustCompile(`^#!\s?/(usr/)?bin/(env\s+)?(sh|bash|mksh|bats|zsh)(\s|$)`)
)

// checked returns whether sizecheck checks the file at path with contents src: a Go, Markdown or
// YAML file, or a shell script.
func checked(path string, src []byte) bool {
	for _, ext := range []string{".go", ".md", ".yml", ".yaml"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return script(path, src)
}

// script returns whether the file at path with contents src is a shell script, by its name or its
// shebang.
func script(path string, src []byte) bool {
	first, _, _ := bytes.Cut(src, []byte("\n"))
	return shellName.MatchString(path) || shellShebang.Match(first)
}

// measure counts the lines of the file at path with contents src and, in Go, its comment blocks.
func measure(path string, src []byte) measurement {
	var m measurement
	m.lines = bytes.Count(src, []byte("\n"))
	if len(src) > 0 && src[len(src)-1] != '\n' {
		m.lines++
	}
	if !strings.HasSuffix(path, ".go") {
		return m
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		m.errLine, m.errMsg = 1, err.Error()
		if list, ok := errors.AsType[scanner.ErrorList](err); ok && len(list) > 0 {
			m.errLine, m.errMsg = list[0].Pos.Line, list[0].Msg
		}
		return m
	}
	for _, group := range file.Comments {
		b := block{line: fset.Position(group.Pos()).Line, length: length(fset, group)}
		switch {
		case group == file.Doc:
			m.pkg = b
		case b.length > maxComment:
			m.blocks = append(m.blocks, b)
		}
	}
	return m
}

// length returns the lines of a comment group that count toward the caps: all but its directives
// and the bare // that gofmt puts before a doc comment's directives.
func length(fset *token.FileSet, group *ast.CommentGroup) int {
	lines := make(map[int]bool)
	for i, c := range group.List {
		next := i+1 < len(group.List) && directive(group.List[i+1].Text)
		if directive(c.Text) || c.Text == "//" && next {
			continue
		}
		for line := fset.Position(c.Pos()).Line; line <= fset.Position(c.End()).Line; line++ {
			lines[line] = true
		}
	}
	return len(lines)
}

// directive returns whether the comment c is a directive: go/ast's rule, and //nolint, which
// golangci-lint reads, as tools/valemirror tells one.
func directive(c string) bool {
	body, ok := strings.CutPrefix(c, "//")
	if !ok {
		return false
	}
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

// findings returns the caps that m, the measurement of the file at path, is over, by line.
func (m measurement) findings(path string) []finding {
	var found []finding
	limit := maxLines
	if path == "README.md" {
		limit = maxReadme
	}
	if m.lines > limit {
		found = append(found, finding{path: path, line: 1,
			msg: fmt.Sprintf("%d lines, over the cap of %d", m.lines, limit)})
	}
	if m.errMsg != "" {
		return append(found, finding{path: path, line: m.errLine, msg: "cannot parse: " + m.errMsg})
	}
	if m.pkg.length > maxPackage {
		found = append(found, finding{path: path, line: m.pkg.line,
			msg: fmt.Sprintf("package comment of %d lines, over the cap of %d", m.pkg.length,
				maxPackage)})
	}
	for _, b := range m.blocks {
		found = append(found, finding{path: path, line: b.line,
			msg: fmt.Sprintf("comment block of %d lines, over the cap of %d", b.length, maxComment)})
	}
	slices.SortStableFunc(found, func(a, b finding) int { return a.line - b.line })
	return found
}

// listFiles returns the files git lists in dir, tracked or untracked but not ignored, sorted.
func listFiles(dir string) ([]string, error) {
	cmd := exec.Command("git", "-C", dir, "ls-files", "-z", "--cached", "--others",
		"--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && len(exit.Stderr) > 0 {
			err = fmt.Errorf("git ls-files: %s", bytes.TrimSpace(exit.Stderr))
		}
		return nil, err
	}
	files := strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 })
	slices.Sort(files)
	return slices.Compact(files), nil
}
