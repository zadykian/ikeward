// Command denycheck keeps an employer's terms out of ikeward: its names, hosts, addresses and
// domains, as a list outside the repository gives them. Run from the repository root:
//
//	go run ./tools/denycheck [-require] [FILE...]
//	go run ./tools/denycheck [-require] -history REVS | -files FILE... | -stdin NAME
//
// It reads the files git lists, new ones included, or those of them a FILE names, and their paths.
// With -history, it reads each commit, tag, tree and file that git rev-list --objects REVS lists.
// With -files, it reads the FILEs as they are, as a release's binaries; with -stdin, its input.
// The list comes from DENYLIST, or else from ~/.config/ikeward/denylist. A finding prints where
// the term is and the term's number, never the term. docs/checks.md says how terms match, and
// what happens with no list. The exit status is 1 for a finding, and 2 for a usage or setup
// error, which go run reports as 1.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	var stdout, stderr strings.Builder
	status := run(".", os.Args[1:], os.Getenv, os.Stdin, &stdout, &stderr)
	if _, err := os.Stdout.WriteString(stdout.String()); err != nil {
		fmt.Fprintln(os.Stderr, "denycheck:", err)
		status = 2
	}
	fmt.Fprint(os.Stderr, stderr.String())
	os.Exit(status)
}

// options are what the command line asks for: at most one of history, files and stdin.
type options struct {
	require        bool
	history, stdin string
	files          bool
	args           []string
}

// run checks the repository at dir as the command line args ask, with the environment getenv
// and the input stdin, and returns the exit status.
func run(dir string, args []string, getenv func(string) string, stdin io.Reader,
	stdout, stderr *strings.Builder) int {
	opts, status := parse(args, stderr)
	if status >= 0 {
		return status
	}
	terms, note, err := load(getenv, opts.require)
	if err != nil {
		fmt.Fprintln(stderr, "denycheck:", err)
		return 2
	}
	if terms == nil {
		fmt.Fprintln(stderr, "denycheck:", note)
		return 0
	}
	var r report
	switch {
	case opts.history != "":
		err = history(dir, opts.history, terms, &r)
	case opts.files:
		err = files(opts.args, terms, &r)
	case opts.stdin != "":
		var data []byte
		if data, err = io.ReadAll(stdin); err == nil {
			r.text(opts.stdin, data, terms)
		}
	default:
		err = tree(dir, opts.args, terms, &r)
	}
	if err != nil {
		fmt.Fprintln(stderr, "denycheck:", err)
		return 2
	}
	for _, line := range r.lines {
		fmt.Fprintln(stdout, line)
	}
	if len(r.lines) > 0 {
		fmt.Fprintf(stderr, "denycheck: %d findings above; the list says which term each number "+
			"is\n", len(r.lines))
		return 1
	}
	return 0
}

// parse reads the command line args. It returns a status of -1 where run goes on, and the exit
// status where it stops: after -h, or on a mistake, which it reports to stderr.
func parse(args []string, stderr *strings.Builder) (options, int) {
	var opts options
	flags := flag.NewFlagSet("denycheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&opts.require, "require", false, "fail where there is no list, as CI does")
	flags.StringVar(&opts.history, "history", "", "check what git rev-list --objects `REVS` lists")
	flags.BoolVar(&opts.files, "files", false, "check the FILEs as they are, listed by git or not")
	flags.StringVar(&opts.stdin, "stdin", "", "check the input, as `NAME`")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: go run ./tools/denycheck [-require] "+
			"[FILE... | -history REVS | -files FILE... | -stdin NAME]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, 0
		}
		return opts, 2
	}
	opts.args = flags.Args()
	modes := 0
	for _, set := range []bool{opts.history != "", opts.files, opts.stdin != ""} {
		if set {
			modes++
		}
	}
	switch {
	case modes > 1:
		fmt.Fprintln(stderr, "denycheck: -history, -files and -stdin go alone")
	case (opts.history != "" || opts.stdin != "") && len(opts.args) > 0:
		fmt.Fprintln(stderr, "denycheck: -history and -stdin take no FILE")
	case opts.files && len(opts.args) == 0:
		fmt.Fprintln(stderr, "denycheck: -files takes a FILE at least")
	default:
		return opts, -1
	}
	return opts, 2
}
