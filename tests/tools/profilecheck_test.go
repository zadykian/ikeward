package tools

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/profile/profiletest"
)

// profile returns the files of a client profile that profiletest makes, by their names:
// vpnclient.p12 and vpnclient.sswan.
func profile(t *testing.T) map[string]string {
	t.Helper()
	files := profiletest.Files(t, "vpnclient", "password")
	return map[string]string{files.P12.Name: string(files.P12.Data),
		files.SSwan.Name: string(files.SSwan.Data)}
}

// profileSteps are a session of tools/profilecheck in one repository, each step on what those
// before it left: the git command it runs first, and what tools/profilecheck prints.
var profileSteps = []struct {
	name   string
	git    []string
	status int
	stdout string
}{
	{name: "no commit yet, the profile ignored"},
	{name: "a commit", git: []string{"commit", "-q", "-m", "chore: a"}},
	{
		name: "the profile added in spite of .gitignore", status: 1,
		git:    []string{"add", "-f", "vpnclient.p12", "vpnclient.sswan"},
		stdout: "vpnclient.p12\nvpnclient.sswan\n",
	},
	{
		name: "the profile committed", git: []string{"commit", "-q", "-m", "chore: b"}, status: 1,
		stdout: "vpnclient.p12\nvpnclient.sswan\n",
	},
	{
		name: "the profile taken out of the index", status: 1,
		git:    []string{"rm", "-q", "--cached", "vpnclient.p12", "vpnclient.sswan"},
		stdout: "vpnclient.p12\nvpnclient.sswan\n",
	},
	{
		name: "the profile gone from HEAD, in its history still", status: 1,
		git:    []string{"commit", "-q", "-m", "chore: c"},
		stdout: "vpnclient.p12\nvpnclient.sswan\n",
	},
}

// TestProfilecheck checks the files of a profile in a repository with ikeward's .gitignore.
func TestProfilecheck(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := repository(t, env)
	ignore, err := os.ReadFile(filepath.Join(root(t), ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, profile(t))
	write(t, dir, map[string]string{".gitignore": string(ignore), "README.md": "# A\n"})
	git(t, dir, env, "add", ".gitignore", "README.md")
	for _, step := range profileSteps {
		if step.git != nil {
			git(t, dir, env, step.git...)
		}
		r := script(t, dir, env, "profilecheck")
		if r.status != step.status || r.stdout != step.stdout {
			t.Errorf("%s: tools/profilecheck = %d, stdout %q, stderr %q; want %d and %q", step.name,
				r.status, r.stdout, r.stderr, step.status, step.stdout)
		}
	}
}

// TestProfilecheckCase checks the files of a profile, a key, a certificate and an archive, by an
// extension of any case, which no .gitignore ignores here. A directory of no repository fails.
func TestProfilecheckCase(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := repository(t, env)
	write(t, dir, map[string]string{"docs/VPNClient.MobileConfig": "<plist/>\n", "a.PFX": "x",
		"server.Key": "x", "ca.pem": "x", "certs/server.CRT": "x", "logs.tar.gz": "x",
		"backup.ZIP": "x", "a.go": "x"})
	r := script(t, dir, env, "profilecheck")
	want := "a.PFX\nbackup.ZIP\nca.pem\ncerts/server.CRT\ndocs/VPNClient.MobileConfig\n" +
		"logs.tar.gz\nserver.Key\n"
	if r.status != 1 || r.stdout != want {
		t.Errorf("tools/profilecheck = %d, stdout %q; want 1 and %q", r.status, r.stdout, want)
	}
	r = script(t, t.TempDir(), env, "profilecheck")
	if r.status != 1 || !strings.Contains(r.stderr, "git cannot read the work tree") {
		t.Errorf("tools/profilecheck outside a repository = %d, stderr %q; want 1", r.status,
			r.stderr)
	}
}

// binarySteps are a session of tools/profilecheck in one repository, each step on what those
// before it left. A step writes files and runs a git command first. Then tools/profilecheck runs
// with args, and prints what stdout says.
var binarySteps = []struct {
	name   string
	write  map[string]string
	git    []string
	args   []string
	status int
	stdout string
}{
	{
		name: "a new binary file", write: map[string]string{"a.dat": "a\x00b"}, status: 1,
		stdout: "a.dat\n",
	},
	{name: "added", git: []string{"add", "a.dat"}, status: 1, stdout: "a.dat\n"},
	{name: "taken out of the index", git: []string{"rm", "-q", "--cached", "a.dat"}, status: 1,
		stdout: "a.dat\n"},
	{name: "gone from disk too", git: []string{"clean", "-q", "-f"}},
	{name: "on another branch", git: []string{"checkout", "-q", "other"}, status: 1,
		stdout: "b.dat\n"},
	{name: "back on main", git: []string{"checkout", "-q", "main"}},
	{name: "every ref's history", args: []string{"--all"}, status: 1, stdout: "b.dat\n"},
	{name: "a .gitleaksignore", write: map[string]string{".gitleaksignore": "a\n"}, status: 1},
}

// TestProfilecheckBinary checks binary files among those git lists, and in the history: HEAD's,
// and every ref's with --all. A .gitleaksignore fails it too.
func TestProfilecheckBinary(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := repository(t, env)
	write(t, dir, map[string]string{"README.md": "# A\n"})
	git(t, dir, env, "add", "README.md")
	git(t, dir, env, "commit", "-q", "-m", "chore: a")
	git(t, dir, env, "checkout", "-q", "-b", "other")
	write(t, dir, map[string]string{"b.dat": "\x00"})
	git(t, dir, env, "add", "b.dat")
	git(t, dir, env, "commit", "-q", "-m", "chore: b")
	git(t, dir, env, "checkout", "-q", "main")
	for _, step := range binarySteps {
		write(t, dir, step.write)
		if step.git != nil {
			git(t, dir, env, step.git...)
		}
		r := script(t, dir, env, "profilecheck", step.args...)
		if r.status != step.status || r.stdout != step.stdout {
			t.Errorf("%s: tools/profilecheck = %d, stdout %q, stderr %q; want %d and %q", step.name,
				r.status, r.stdout, r.stderr, step.status, step.stdout)
		}
	}
	if r := script(t, dir, env, "profilecheck"); !strings.Contains(r.stderr, ".gitleaksignore") {
		t.Errorf("tools/profilecheck with a .gitleaksignore: stderr %q, want it named", r.stderr)
	}
}

// wrapped returns the .sswan with its p12 wrapped as ikev2setup.sh writes it: at 52 characters,
// the lines joined by \n escapes.
func wrapped(t *testing.T, sswan string) string {
	t.Helper()
	before, rest, ok := strings.Cut(sswan, `"p12": "`)
	p12, after, ok2 := strings.Cut(rest, `"`)
	if !ok || !ok2 || len(p12) <= 52 {
		t.Fatalf("no p12 to wrap in the .sswan:\n%s", sswan)
	}
	var lines []string
	for ; len(p12) > 52; p12 = p12[52:] {
		lines = append(lines, p12[:52])
	}
	return before + `"p12": "` + strings.Join(append(lines, p12), `\n`) + `"` + after
}

// TestGitleaks runs make gitleaks, with .gitleaks.toml, on a repository that commits a profile's
// .sswan, its p12 wrapped, and a server's certificate and key. A copy by another name holds the
// p12 on one line, with a gitleaks:allow comment, which make gitleaks ignores. tools/profilecheck
// is a stub that passes. The file's rules find each p12 and PEM block, and gitleaks' the key.
func TestGitleaks(t *testing.T) {
	t.Parallel()
	env := environ(t)
	dir := repository(t, env)
	cert, key := profiletest.NewCA(t).Server(t)
	sswan := profile(t)["vpnclient.sswan"]
	config, err := os.ReadFile(filepath.Join(root(t), ".gitleaks.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var allowed []string
	for line := range strings.Lines(sswan) {
		if strings.Contains(line, `"p12"`) {
			line = strings.TrimSuffix(line, "\n") + " // gitleaks:allow\n"
		}
		allowed = append(allowed, line)
	}
	write(t, dir, map[string]string{
		"vpnclient.sswan": wrapped(t, sswan), "vpnclient.json": strings.Join(allowed, ""),
		"server": string(cert) + string(key), ".gitleaks.toml": string(config),
	})
	executable(t, dir, "tools/profilecheck", "#!/bin/sh\n")
	executable(t, dir, "tools/run", "#!/bin/sh\nexec '"+filepath.Join(root(t), "tools", "run")+
		"' \"$@\"\n")
	git(t, dir, env, "add", "vpnclient.sswan", "vpnclient.json", "server")
	git(t, dir, env, "commit", "-q", "-m", "chore: a")
	r := command(t, dir, env, "make", "-s", "-f", filepath.Join(root(t), "Makefile"), "gitleaks")
	var got []string
	rule := ""
	for line := range strings.Lines(r.stdout) {
		if value, ok := strings.CutPrefix(line, "RuleID:"); ok {
			rule = strings.TrimSpace(value)
		} else if value, ok := strings.CutPrefix(line, "File:"); ok {
			got = append(got, strings.TrimSpace(value)+": "+rule)
		}
	}
	slices.Sort(got)
	want := []string{"server: pem-block", "server: pem-block", "server: private-key",
		"vpnclient.json: sswan-p12", "vpnclient.sswan: sswan-p12"}
	if r.status != 2 || !slices.Equal(got, want) {
		t.Errorf("make gitleaks = %d, found %q, stderr:\n%s\nwant 2 and %q", r.status, got,
			r.stderr, want)
	}
}

// executable writes the script at path in dir, executable.
func executable(t *testing.T, dir, path, script string) {
	t.Helper()
	write(t, dir, map[string]string{path: script})
	if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(path)), 0o755); err != nil {
		t.Fatal(err)
	}
}
