package hooks

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// lintLocal is fakeMake's run as the Stop hook runs it.
var lintLocal = []string{"make", "-k", "lint-local"}

// failed is the Stop hook's stderr where fakeMake fails: the targets, then the end of the output.
const failed = "make -k lint-local fails at:\n" +
	"make: *** [Makefile:40: vet] Error 1\nmake: *** [Makefile:93: zizmor] Error 1\n" +
	"Its output ends with:\na.go:1: a finding\n" +
	"make: *** [Makefile:40: vet] Error 1\nmake: *** [Makefile:93: zizmor] Error 1\n" +
	"make: Target 'lint-local' not remade because of errors.\n"

// event is the JSON that Claude Code pipes into lint-branch.sh as a session starts, as Claude
// ends its turn, or for another event, with stop_hook_active as active says.
func event(name string, active bool) map[string]any {
	e := map[string]any{"session_id": "a", "transcript_path": "/a.jsonl", "cwd": "/",
		"hook_event_name": name}
	switch name {
	case "SessionStart":
		e["source"] = "startup"
	case "Stop":
		e["stop_hook_active"] = active
		e["last_assistant_message"] = `Done: "stop_hook_active": true is set.`
	}
	return e
}

// sessionSteps are a session of Claude's in the main work tree, each step on what those before it
// left. A step writes, moves or commits files, then pipes an event into lint-branch.sh. ran says
// whether make -k lint-local ran, and fail makes it fail.
var sessionSteps = []struct {
	name                      string
	write                     map[string]string
	move                      [2]string // a file's old and new path
	commit                    []string  // git commit's arguments, after git add -A
	event                     string
	active, indent, fail, ran bool
	stderr                    string
}{
	{
		name: "the session starts on work in progress", write: map[string]string{"docs/a.md": "# B\n"},
		event: "SessionStart",
	},
	{name: "a turn that changes nothing", event: "Stop"},
	{
		name: "a turn that writes a new file, and make fails", event: "Stop",
		write: map[string]string{"docs/new.md": "# New\n"}, fail: true, ran: true, stderr: failed,
	},
	{name: "the turn that goes on", event: "Stop", active: true, indent: true},
	{name: "the next turn, with nothing changed since the failure", event: "Stop"},
	{
		name: "a turn that fixes it, and make passes", write: map[string]string{"docs/new.md": "# N\n"},
		event: "Stop", ran: true,
	},
	{name: "a turn that changes nothing since the pass", event: "Stop"},
	{
		name: "a turn that writes a file git ignores", event: "Stop",
		write: map[string]string{".claude/plans/next.md": "# Next\n"},
	},
	{
		name: "a turn that edits a tracked file", write: map[string]string{"docs/a.md": "# C\n"},
		event: "Stop", ran: true,
	},
	{
		name: "a turn that changes a new file", write: map[string]string{"docs/new.md": "# Newer\n"},
		event: "Stop", ran: true,
	},
	{
		name: "a turn that renames it", move: [2]string{"docs/new.md", "docs/moved.md"},
		event: "Stop", ran: true,
	},
	{name: "a turn that commits it all", commit: []string{}, event: "Stop", ran: true},
	{
		name: "a turn that commits a message alone", commit: []string{"--allow-empty"},
		event: "Stop", ran: true,
	},
	{name: "another event", write: map[string]string{"docs/c.md": "# C\n"}, event: "Notification"},
}

func TestLintBranch(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, step := range sessionSteps {
		write(t, w.repo, step.write)
		if step.move[0] != "" {
			err := os.Rename(filepath.Join(w.repo, step.move[0]), filepath.Join(w.repo, step.move[1]))
			if err != nil {
				t.Fatal(err)
			}
		}
		if step.commit != nil {
			git(t, w.repo, "add", "-A")
			git(t, w.repo, append([]string{"commit", "-q", "-m", "docs: a change"}, step.commit...)...)
		}
		var fail []string
		if step.fail {
			fail = []string{"*"}
		}
		input := encode(t, event(step.event, step.active), step.indent)
		r := w.hook(t, "lint-branch.sh", w.repo, input, fail...)
		var want []run
		if step.ran {
			want = []run{{dir: w.repo, args: lintLocal}}
		}
		if got := w.runs(t); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the fakes ran %q, want %q", step.name, got, want)
		}
		status := 0
		if step.fail {
			status = 2
		}
		if r.status != status || r.stdout != "" || r.stderr != step.stderr {
			t.Errorf("%s: status %d, stdout %q, stderr\n%s\nwant %d, nothing, and\n%s", step.name,
				r.status, r.stdout, r.stderr, status, step.stderr)
		}
	}
}

// TestLintBranchWorkTrees runs lint-branch.sh in the work trees of the repository, from a
// subdirectory too, and in other directories. Each work tree keeps its own notes, and make runs
// in the work tree Claude is in, or nowhere.
func TestLintBranchWorkTrees(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	start := encode(t, event("SessionStart", false), false)
	stop := encode(t, event("Stop", false), false)
	for _, dir := range []string{w.repo, w.linked} {
		w.hook(t, "lint-branch.sh", dir, start)
	}
	note := filepath.Join(w.repo, ".git", "worktrees", "linked", "claude-lint-start")
	if _, err := os.Stat(note); err != nil {
		t.Errorf("no note in the linked work tree's git directory: %v", err)
	}
	for _, dir := range []string{w.linked, w.old, w.other} {
		write(t, dir, map[string]string{"docs/new.md": "# New\n"})
	}
	for _, step := range []struct {
		name, dir string
		want      []run
	}{
		{name: "the main work tree, unchanged", dir: w.repo},
		{
			name: "a subdirectory of the linked work tree, changed", dir: filepath.Join(w.linked, "docs"),
			want: []run{{dir: w.linked, args: lintLocal}},
		},
		{name: "a work tree of a branch from before make lint-local", dir: w.old},
		{name: "another repository, changed", dir: w.other},
		{name: "a directory of no repository", dir: w.outside},
	} {
		r := w.hook(t, "lint-branch.sh", step.dir, stop)
		if got := w.runs(t); r.status != 0 || !reflect.DeepEqual(got, step.want) {
			t.Errorf("%s: status %d, stderr %q, the fakes ran %q; want 0 and %q", step.name,
				r.status, r.stderr, got, step.want)
		}
	}
}
