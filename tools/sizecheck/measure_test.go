package main

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// goSource is a Go file of a package comment, a blank line, body and a declaration.
func goSource(doc, body string) string {
	return doc + "package a\n\n" + body + "var x int\n"
}

// comment returns a // comment of n lines.
func comment(n int) string {
	return strings.Repeat("// x\n", n)
}

var measureTests = []struct {
	name, path, src string
	want            measurement
	wantErrLine     int // the line of a parse error, 0 for none
}{
	{name: "empty", path: "a.md", src: ""},
	{name: "lines", path: "a.md", src: "a\n\nb\n", want: measurement{lines: 3}},
	{name: "no final newline", path: "a.md", src: "a\nb", want: measurement{lines: 2}},
	{
		name: "markdown has no comment blocks", path: "a.md", src: comment(6),
		want: measurement{lines: 6},
	},
	{
		name: "a block at the cap", path: "a.go", src: goSource("", comment(4)),
		want: measurement{lines: 7},
	},
	{
		name: "a block over the cap", path: "a.go", src: goSource("", comment(5)),
		want: measurement{lines: 8, blocks: []block{{3, 5}}},
	},
	{
		name: "a /* */ block over the cap", path: "a.go", src: goSource("", "/*\n1\n2\n3\n*/\n"),
		want: measurement{lines: 8, blocks: []block{{3, 5}}},
	},
	{
		name: "a blank line splits blocks", path: "a.go",
		src:  goSource("", comment(3)+"\n"+comment(3)),
		want: measurement{lines: 10},
	},
	{
		name: "a trailing comment is a block of its own", path: "a.go",
		src:  goSource("", "var y int // y\n"+comment(4)),
		want: measurement{lines: 8},
	},
	{
		name: "blocks inside a function", path: "a.go",
		src:  goSource("", "func f() {\n"+comment(6)+"}\n"),
		want: measurement{lines: 11, blocks: []block{{4, 6}}},
	},
	{
		name: "the package comment at its cap", path: "a.go", src: goSource(comment(15), ""),
		want: measurement{lines: 18, pkg: block{1, 15}},
	},
	{
		name: "the package comment over its cap", path: "a.go",
		src:  goSource("//go:build linux\n\n"+comment(16), comment(5)),
		want: measurement{lines: 26, pkg: block{3, 16}, blocks: []block{{21, 5}}},
	},
	{
		name: "a comment above the package clause, apart from it", path: "a.go",
		src:  comment(5) + "\n" + goSource("", ""),
		want: measurement{lines: 9, blocks: []block{{1, 5}}},
	},
	{
		name: "a doc comment at the cap with its directives and the // gofmt puts before them",
		path: "a.go",
		src:  goSource("", comment(4)+"//\n//nolint:funlen // why\n//go:noinline\nfunc f() {}\n"),
		want: measurement{lines: 11},
	},
	{
		name: "directives leave a block over the cap over it", path: "a.go",
		src:  goSource("", comment(5)+"//\n//nolint:funlen // why\nfunc f() {}\n"),
		want: measurement{lines: 11, blocks: []block{{3, 5}}},
	},
	{
		name: "a bare // with no directive after it counts", path: "a.go",
		src:  goSource("", comment(4)+"//\nfunc f() {}\n"),
		want: measurement{lines: 9, blocks: []block{{3, 5}}},
	},
	{
		name: "a parse error", path: "a.go", src: "package a\n\nfunc {\n",
		want: measurement{lines: 3}, wantErrLine: 3,
	},
}

func TestMeasure(t *testing.T) {
	t.Parallel()
	for _, tt := range measureTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := measure(tt.path, []byte(tt.src))
			if (got.errMsg != "") != (tt.wantErrLine != 0) || got.errLine != tt.wantErrLine {
				t.Errorf("parse error at %d (%q), want one at %d", got.errLine, got.errMsg,
					tt.wantErrLine)
			}
			got.errLine, got.errMsg = 0, ""
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("measure(%q) = %+v, want %+v", tt.src, got, tt.want)
			}
		})
	}
}

// findingsTests are measurements of the file at path, a.go where none is given, and their findings.
var findingsTests = []struct {
	name, path string
	m          measurement
	want       []string
}{
	{name: "within the caps", m: measurement{lines: 300, pkg: block{1, 15}}},
	{name: "the README at its cap", path: "README.md", m: measurement{lines: 150}},
	{
		name: "the README over its cap", path: "README.md", m: measurement{lines: 151},
		want: []string{"README.md:1: 151 lines, over the cap of 150"},
	},
	{name: "a README elsewhere", path: "docs/README.md", m: measurement{lines: 300}},
	{
		name: "over every cap, by line",
		m:    measurement{lines: 310, pkg: block{3, 20}, blocks: []block{{30, 5}, {40, 6}}},
		want: []string{
			"a.go:1: 310 lines, over the cap of 300",
			"a.go:3: package comment of 20 lines, over the cap of 15",
			"a.go:30: comment block of 5 lines, over the cap of 4",
			"a.go:40: comment block of 6 lines, over the cap of 4",
		},
	},
	{
		name: "a parse error",
		m:    measurement{lines: 301, errLine: 5, errMsg: "bad"},
		want: []string{"a.go:1: 301 lines, over the cap of 300", "a.go:5: cannot parse: bad"},
	},
}

func TestFindings(t *testing.T) {
	t.Parallel()
	for _, tt := range findingsTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := tt.path
			if path == "" {
				path = "a.go"
			}
			var got []string
			for _, f := range tt.m.findings(path) {
				got = append(got, fmt.Sprintf("%s:%d: %s", f.path, f.line, f.msg))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("findings:\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// checkedTests are files by their path and first line, and whether sizecheck checks each.
var checkedTests = []struct {
	path, first string
	want        bool
}{
	{"a.go", "package a", true},
	{"docs/a.md", "# A", true},
	{".github/workflows/ci.yml", "name: ci", true},
	{"compose.yaml", "services:", true},
	{"install.sh", "", true},
	{"tools/run", "#!/bin/sh", true},
	{"hook", "#!/usr/bin/env bash", true},
	{"hook", "#! /bin/bash -e", true},
	{".probe.sh", "echo", true},
	{".dir/a.sh", "echo", true},
	{"tools/hook.txt", "#!/bin/sh", true},
	{"script", "#!/usr/bin/env python3", false},
	{"Makefile", "VERSION ?= dev", false},
	{"go.mod", "module a", false},
	{"Dockerfile", "FROM ubuntu:26.04", false},
}

func TestChecked(t *testing.T) {
	t.Parallel()
	for _, tt := range checkedTests {
		if got := checked(tt.path, []byte(tt.first+"\nrest\n")); got != tt.want {
			t.Errorf("checked(%q, %q) = %v, want %v", tt.path, tt.first, got, tt.want)
		}
	}
}
