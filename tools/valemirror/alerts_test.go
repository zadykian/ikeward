package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// translateIndex has a mirror of a.go with two pieces on line 3. They start at the mirror's columns
// 3 and 10, and at the source's 7 and 20.
var translateIndex = map[string]source{
	"0/a.go.md": {File: "dir/a.go", Lines: map[int][]piece{3: {{3, 7}, {10, 20}}}},
}

// translateTests are lines of Vale's output, $DIR standing for the mirrors' directory, and the
// lines translate writes for them.
var translateTests = []struct {
	name, alert, want string
}{
	{
		name:  "a Markdown file's alert stays",
		alert: "README.md:3:5:Rule:A message.",
		want:  "README.md:3:5:Rule:A message.",
	},
	{
		name:  "in the first piece",
		alert: "$DIR/0/a.go.md:3:5:Rule:A message: with a colon.",
		want:  "dir/a.go:3:9:Rule:A message: with a colon.",
	},
	{
		name:  "in the second piece",
		alert: "$DIR/0/a.go.md:3:12:Rule:A message.",
		want:  "dir/a.go:3:22:Rule:A message.",
	},
	{
		name:  "before the first piece",
		alert: "$DIR/0/a.go.md:3:1:Rule:A message.",
		want:  "dir/a.go:3:7:Rule:A message.",
	},
	{
		name:  "a line without pieces keeps its column",
		alert: "$DIR/0/a.go.md:4:2:Rule:A message.",
		want:  "dir/a.go:4:2:Rule:A message.",
	},
	{
		name:  "a mirror the index lacks",
		alert: "$DIR/1/b.go.md:3:5:Rule:A message.",
		want:  "$DIR/1/b.go.md:3:5:Rule:A message.",
	},
	{
		name:  "a line Vale did not write",
		alert: "$DIR/0/a.go.md:x:5:Rule:A message.",
		want:  "$DIR/0/a.go.md:x:5:Rule:A message.",
	},
}

func TestTranslate(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "mirrors")
	for _, tt := range translateTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := strings.ReplaceAll(tt.alert, "$DIR", dir) + "\n"
			want := strings.ReplaceAll(tt.want, "$DIR", dir) + "\n"
			var out strings.Builder
			if err := translate(dir, translateIndex, strings.NewReader(in), &out); err != nil {
				t.Fatal(err)
			}
			if out.String() != want {
				t.Errorf("translate(%q) = %q, want %q", in, out.String(), want)
			}
		})
	}
}
