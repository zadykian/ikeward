package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// maxLines is the cap of a file's lines.
const maxLines = 300

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

// measure counts the lines of src, a last one without a newline among them.
func measure(src []byte) int {
	lines := bytes.Count(src, []byte("\n"))
	if len(src) > 0 && src[len(src)-1] != '\n' {
		lines++
	}
	return lines
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
