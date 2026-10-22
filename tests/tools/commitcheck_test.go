package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// long is 70 columns of a trailer's value or a line of the body.
var long = strings.Repeat("x", 70)

// headerFault is commitcheck's fault for a header of the wrong form.
const headerFault = "1: the header is not type(scope)!: description, with a type of feat, fix, " +
	"build, chore, ci, docs, perf, refactor, revert, style or test"

// commitTests are commit messages, and the faults that tools/commitcheck finds in each, by line.
var commitTests = []struct {
	name, message string
	faults        []string
}{
	{name: "a header and a body", message: "fix: the check\n\n- what was missing\n"},
	{name: "a scope and a !", message: "feat(files)!: a change\n"},
	{name: "a revert", message: "revert: the check\n\nRefs: 1234567\n"},
	{name: "a revert with a scope", message: "revert(files): a change\n"},
	{name: "an unknown type", message: "feature: the check\n", faults: []string{headerFault}},
	{name: "an upper-case type", message: "Fix: the check\n", faults: []string{headerFault}},
	{name: "git's revert", message: "Revert \"fix: the check\"\n", faults: []string{headerFault}},
	{name: "no description", message: "fix:  the check\n", faults: []string{headerFault}},
	{
		name: "a header that ends in a period", message: "fix: the check.\n",
		faults: []string{"1: the header ends in a period"},
	},
	{name: "a period inside the header", message: "fix: v1.2 and the check\n"},
	{
		name: "a header over 72 columns", message: "fix: " + long[:68] + "\n",
		faults: []string{"1: the header is 73 columns, over 72"},
	},
	{
		name: "no blank line after the header", message: "fix: a\nthe body\n",
		faults: []string{"2: the line after the header is not blank"},
	},
	{
		name: "a line of the body over 72 columns", message: "fix: a\n\n- " + long + "x\n",
		faults: []string{"3: the line is 73 columns, over 72"},
	},
	{name: "72 runes of two bytes", message: "fix: a\n\n- " + strings.Repeat("é", 70) + "\n"},
	{name: "a line with a URL", message: "fix: a\n\n- https://example.org/" + long + "\n"},
	{
		name: "the co-author and sign-off trailers",
		message: "fix: a\n\n- b\n\nCo-Authored-By: " + long + " <a@example.org>\n" +
			"signed-off-by: " + long + " <a@example.org>\n",
	},
	{
		name: "a paragraph of other trailers, as git reads them",
		message: "fix: a\n\nMissing: " + long + "\nChecked: " + long + "\n" +
			"Co-Authored-By: A <a@example.org>\n",
		faults: []string{"3: the line is 79 columns, over 72", "4: the line is 79 columns, over 72"},
	},
}

// commit commits the message in dir, with --allow-empty, and returns the commit's short hash.
func commit(t *testing.T, dir string, env []string, message string, args ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "message")
	if err := os.WriteFile(path, []byte(message), 0o644); err != nil {
		t.Fatal(err)
	}
	args = append([]string{"commit", "-q", "--allow-empty", "--cleanup=verbatim", "-F", path}, args...)
	git(t, dir, env, args...)
	return strings.TrimSpace(git(t, dir, env, "rev-parse", "--short", "HEAD"))
}

// based makes a repository with a commit on main, and the branch base at it, the base of the
// commits that a test checks.
func based(t *testing.T, env []string) string {
	t.Helper()
	dir := repository(t, env)
	commit(t, dir, env, "chore: the base\n")
	git(t, dir, env, "branch", "-q", "base")
	return dir
}

func TestCommitcheck(t *testing.T) {
	t.Parallel()
	for _, tt := range commitTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := environ(t)
			dir := based(t, env)
			hash := commit(t, dir, env, tt.message)
			r := script(t, dir, env, "commitcheck", "base")
			var want []string
			for _, fault := range tt.faults {
				want = append(want, hash+":"+fault)
			}
			status := 0
			if len(want) > 0 {
				status = 1
			}
			got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
			if r.stdout == "" {
				got = nil
			}
			if r.status != status || !slices.Equal(got, want) {
				t.Errorf("tools/commitcheck = %d, stdout\n%s\nstderr %q; want %d and\n%s", r.status,
					r.stdout, r.stderr, status, strings.Join(want, "\n"))
			}
		})
	}
}

// TestCommitcheckRange checks the commits after BASE alone, every one, and passes over merges.
func TestCommitcheckRange(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := based(t, env)
	commit(t, dir, env, "Bad base\n")
	git(t, dir, env, "branch", "-q", "-f", "base")
	git(t, dir, env, "switch", "-q", "-c", "side")
	first := commit(t, dir, env, "Bad one\n")
	git(t, dir, env, "switch", "-q", "main")
	second := commit(t, dir, env, "fix: a\nBad two\n")
	git(t, dir, env, "merge", "-q", "--no-ff", "-m", "Merge side", "side")
	r := script(t, dir, env, "commitcheck", "base")
	want := []string{first + ":" + headerFault, second + ":2: the line after the header is not blank"}
	got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	slices.Sort(got)
	slices.Sort(want)
	if r.status != 1 || !slices.Equal(got, want) {
		t.Errorf("tools/commitcheck base = %d, stdout\n%s\nwant 1 and, in any order,\n%s", r.status,
			r.stdout, strings.Join(want, "\n"))
	}
}

// TestCommitcheckUsage checks a missing base and a command line of two.
func TestCommitcheckUsage(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := based(t, env)
	for args, inStderr := range map[string]string{
		"":       "no commit origin/main: fetch it",
		"nosuch": "no commit nosuch",
		"a b":    "usage: tools/commitcheck [BASE]",
	} {
		r := script(t, dir, env, "commitcheck", strings.Fields(args)...)
		if r.status != 1 || r.stdout != "" || !strings.Contains(r.stderr, inStderr) {
			t.Errorf("tools/commitcheck %s = %d, stdout %q, stderr %q; want 1 and %q", args,
				r.status, r.stdout, r.stderr, inStderr)
		}
	}
}

// TestCommitcheckSigned checks a signed commit where git's log.showSignature is set, which would
// put the signature's check before the message.
func TestCommitcheckSigned(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen to make a signing key:", err)
	}
	env := environ(t)
	dir := based(t, env)
	key := filepath.Join(t.TempDir(), "key")
	command(t, dir, env, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	public, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	signers := filepath.Join(t.TempDir(), "allowed")
	line := "test@example.org namespaces=\"git\" " + string(public)
	if err := os.WriteFile(signers, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, setting := range [][2]string{{"gpg.format", "ssh"}, {"user.signingkey", key + ".pub"},
		{"gpg.ssh.allowedSignersFile", signers}, {"log.showSignature", "true"}} {
		git(t, dir, env, "config", setting[0], setting[1])
	}
	commit(t, dir, env, "fix: a signed commit\n\n- its body\n", "-S")
	if out := git(t, dir, env, "show", "-s", "--format=%B"); !strings.Contains(out, "signature") {
		t.Fatalf("git show prints no signature's check:\n%s", out)
	}
	if r := script(t, dir, env, "commitcheck", "base"); r.status != 0 || r.stdout != "" {
		t.Errorf("tools/commitcheck = %d, stdout\n%s\nstderr %q; want 0", r.status, r.stdout,
			r.stderr)
	}
}
