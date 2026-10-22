package install

import (
	"fmt"
	"strings"
	"testing"
)

// seq is the lines from 1 to n, with the line i replaced where change has i.
func seq(n int, change map[int]string) string {
	var out strings.Builder
	for i := 1; i <= n; i++ {
		line, ok := change[i]
		if !ok {
			line = fmt.Sprint(i)
		}
		out.WriteString(line + "\n")
	}
	return out.String()
}

// unified prints what GNU diff -u does, but for the times; each want is diff 3.12's.
func TestUnified(t *testing.T) {
	for _, test := range []struct {
		name, old, new, want string
	}{
		{"one change", seq(20, nil), seq(20, map[int]string{10: "ten"}),
			"@@ -7,7 +7,7 @@\n 7\n 8\n 9\n-10\n+ten\n 11\n 12\n 13\n"},
		{"joined", seq(20, nil), seq(20, map[int]string{5: "five", 12: "twelve"}),
			"@@ -2,14 +2,14 @@\n 2\n 3\n 4\n-5\n+five\n 6\n 7\n 8\n 9\n 10\n 11\n-12\n+twelve\n" +
				" 13\n 14\n 15\n"},
		{"apart", seq(20, nil), seq(20, map[int]string{5: "five", 13: "thirteen"}),
			"@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+five\n 6\n 7\n 8\n" +
				"@@ -10,7 +10,7 @@\n 10\n 11\n 12\n-13\n+thirteen\n 14\n 15\n 16\n"},
		{"ends", seq(10, nil), seq(10, map[int]string{1: "one", 10: "ten"}),
			"@@ -1,4 +1,4 @@\n-1\n+one\n 2\n 3\n 4\n@@ -7,4 +7,4 @@\n 7\n 8\n 9\n-10\n+ten\n"},
		{"no newline", "a\nb\nc", "a\nb\nc\n",
			"@@ -1,3 +1,3 @@\n a\n b\n-c\n\\ No newline at end of file\n+c\n"},
		{"emptied", "a\nb\n", "", "@@ -1,2 +0,0 @@\n-a\n-b\n"},
		{"added", seq(8, nil), seq(10, nil), "@@ -6,3 +6,5 @@\n 6\n 7\n 8\n+9\n+10\n"},
		{"removed and added", seq(8, nil), "1\n2\n3\n5\n6\nsix\n7\n8\n",
			"@@ -1,8 +1,8 @@\n 1\n 2\n 3\n-4\n 5\n 6\n+six\n 7\n 8\n"},
		{"one line", "x\n", "y\n", "@@ -1 +1 @@\n-x\n+y\n"},
		{"new", "", "a\nb\nc", "@@ -0,0 +1,3 @@\n+a\n+b\n+c\n\\ No newline at end of file\n"},
		{"same", "a\n", "a\n", ""},
	} {
		got := unified("from", "to", []byte(test.old), []byte(test.new))
		if want := "--- from\n+++ to\n" + test.want; got != want {
			t.Errorf("%s:\n%s\nwant\n%s", test.name, got, want)
		}
	}
}
