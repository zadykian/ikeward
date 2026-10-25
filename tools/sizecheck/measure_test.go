package main

import "testing"

var measureTests = []struct {
	name, src string
	want      int
}{
	{name: "empty", src: ""},
	{name: "lines", src: "a\n\nb\n", want: 3},
	{name: "no final newline", src: "a\nb", want: 2},
}

func TestMeasure(t *testing.T) {
	t.Parallel()
	for _, tt := range measureTests {
		if got := measure([]byte(tt.src)); got != tt.want {
			t.Errorf("%s: measure(%q) = %d, want %d", tt.name, tt.src, got, tt.want)
		}
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
