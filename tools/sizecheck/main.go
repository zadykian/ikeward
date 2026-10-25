// Command sizecheck keeps ikeward's files short.
//
// Run from the repository root:
//
//	go run ./tools/sizecheck [-scripts] [FILE...]
//
// Every Go, Markdown, YAML and shell file is at most 300 lines. It checks the files git lists, new
// ones included, or those of them a FILE names. It passes over the vendored files under
// .vale/styles, which a bump replaces whole. With -scripts, it prints the shell scripts among them
// instead, for make vet to check. Findings go to stdout as PATH:LINE: message. The exit status is 1
// for a finding, and 2 for a usage or setup error, which go run reports as 1.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	var stdout, stderr strings.Builder
	status := run(".", os.Args[1:], &stdout, &stderr)
	if _, err := os.Stdout.WriteString(stdout.String()); err != nil {
		fmt.Fprintln(os.Stderr, "sizecheck:", err)
		status = 2
	}
	fmt.Fprint(os.Stderr, stderr.String())
	os.Exit(status)
}

// run checks the repository at dir as the command line args ask, and returns the exit status.
func run(dir string, args []string, stdout, stderr *strings.Builder) int {
	flags := flag.NewFlagSet("sizecheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	scripts := flags.Bool("scripts", false, "print the shell scripts instead of checking them")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: go run ./tools/sizecheck [-scripts] [FILE...]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	files, err := inspect(dir, flags.Args())
	if err != nil {
		fmt.Fprintln(stderr, "sizecheck:", err)
		return 2
	}
	if *scripts {
		return printScripts(files, flags.NArg() == 0, stdout, stderr)
	}
	var findings []finding
	for _, f := range files {
		if n := measure(f.src); n > maxLines && checked(f.path, f.src) {
			findings = append(findings, finding{path: f.path, line: 1,
				msg: fmt.Sprintf("%d lines, over the cap of %d", n, maxLines)})
		}
	}
	for _, f := range findings {
		fmt.Fprintf(stdout, "%s:%d: %s\n", f.path, f.line, f.msg)
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

// printScripts prints the paths of the shell scripts among files, and returns the exit status.
// all tells that files are every one git lists. Where those hold no script, the status is 2, as
// make vet would check none.
func printScripts(files []file, all bool, stdout, stderr *strings.Builder) int {
	n := 0
	for _, f := range files {
		if script(f.path, f.src) {
			fmt.Fprintln(stdout, f.path)
			n++
		}
	}
	if n == 0 && all {
		fmt.Fprintln(stderr, "sizecheck: no shell script among the files git lists")
		return 2
	}
	return 0
}

// A file is one in scope: its path from the repository root, and its contents.
type file struct {
	path string
	src  []byte
}

// inspect returns the files in scope, sorted by path: those git lists, or those of them that args
// name. It passes over the vendored ones, and those that are not regular files on disk.
func inspect(dir string, args []string) ([]file, error) {
	listed, err := listFiles(dir)
	if err != nil {
		return nil, err
	}
	inScope, err := scope(dir, args)
	if err != nil {
		return nil, err
	}
	var files []file
	for _, path := range listed {
		if !inScope(path) || vendored(path) {
			continue
		}
		src, ok, err := read(dir, path)
		if err != nil {
			return nil, err
		}
		if ok {
			files = append(files, file{path: path, src: src})
		}
	}
	return files, nil
}

// read returns the contents of the listed file at path from dir, and false for a file gone from
// disk or not a regular file. A nested repository or work tree is listed as DIR/, and a submodule
// or a symlink to a directory by its name.
func read(dir, path string) ([]byte, bool, error) {
	if strings.HasSuffix(path, "/") {
		return nil, false, nil
	}
	name := filepath.Join(dir, filepath.FromSlash(path))
	info, err := os.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, false, err
	}
	src, err := os.ReadFile(name)
	return src, err == nil, err
}

// vendoredDirs are the directories, from the repository root, of the vendored files, each with
// its authors' LICENSE: the Vale styles. No other file is passed over.
var vendoredDirs = []string{".vale/styles/"}

// vendored returns whether a path from the repository root is in a vendored directory.
func vendored(path string) bool {
	for _, dir := range vendoredDirs {
		if strings.HasPrefix(path, dir) {
			return true
		}
	}
	return false
}

// scope returns whether a path from the repository root is among the FILE arguments, all paths
// when there are none. An argument outside dir names nothing.
func scope(dir string, args []string) (func(string) bool, error) {
	if len(args) == 0 {
		return func(string) bool { return true }, nil
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]bool)
	for _, arg := range args {
		path := arg
		if filepath.IsAbs(arg) {
			if path, err = filepath.Rel(root, arg); err != nil {
				continue
			}
		}
		path = filepath.ToSlash(filepath.Clean(path))
		if path != ".." && !strings.HasPrefix(path, "../") {
			paths[path] = true
		}
	}
	return func(path string) bool { return paths[path] }, nil
}
