package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
)

var allFiles = []string{
	"/etc/strongswan.d/zz-ikeward.conf",
	"/etc/swanctl/conf.d/ikeward.conf",
	"/etc/swanctl/private/client-key.pem",
	"/etc/swanctl/x509/client-cert.pem",
	"/etc/swanctl/x509ca/ikeward-ca.pem",
	"/etc/systemd/network/25-ikeward0.netdev",
	"/etc/systemd/network/25-ikeward0.network",
	"/etc/systemd/networkd.conf.d/10-keep-vpn-routing.conf",
	"/etc/systemd/system/ikeward-routing.service",
	"/etc/systemd/system/ikeward-watchdog.service",
	"/etc/systemd/system/ikeward-watchdog.timer",
	install.Record,
	files.SettingsFile,
}

// install writes the files under the root directory, the key for root alone, and runs the
// commands. uninstall removes them again, with the NAT table, ikeward's rules, table 220's routes
// and the tunnel's link. The .p12's password comes from standard input.
func TestInstallCommand(t *testing.T) {
	f := newFakeSystem(t, "secret\n")
	if err := run(installArgs(writeProfile(t, "secret")), f.system); err != nil {
		t.Fatal(err)
	}
	if got := f.installed(t); !slices.Equal(got, allFiles) {
		t.Errorf("install wrote %q, want %q", got, allFiles)
	}
	if got := f.mode(t, "/etc/swanctl/private/client-key.pem"); got != 0o600 {
		t.Errorf("the key's mode is %04o, want 0600", got)
	}
	if got := f.mode(t, "/etc/swanctl/private"); got != 0o700 {
		t.Errorf("the key's directory's mode is %04o, want 0700", got)
	}
	for _, want := range []string{"The profile gives:\n", "which has a password\n",
		"Created /etc/swanctl/private/client-key.pem\n",
		"Running swanctl --load-all --noprompt\n"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("the output lacks %q:\n%s", want, f.out.String())
		}
	}
	if f.term.reads > 0 || f.errOut.Len() > 0 {
		t.Errorf("read the terminal %d times, and printed %q", f.term.reads, f.errOut.String())
	}
	if !slices.Contains(f.ran.commands, "swanctl --load-all --noprompt") {
		t.Errorf("ran %q", f.ran.commands)
	}

	f.out.Reset()
	if err := run([]string{"uninstall"}, f.system); err != nil {
		t.Fatal(err)
	}
	if got := f.installed(t); len(got) > 0 {
		t.Errorf("uninstall left %q", got)
	}
	if !slices.Equal(f.nat.calls, []string{"remove ikeward"}) {
		t.Errorf("uninstall did %q to the tables, want ikeward removed", f.nat.calls)
	}
	wantChanges := []string{"remove rule 32000: from all lookup main suppress_prefixlength 0",
		"remove rule 32001: from all lookup 220 suppress_prefixlength 0", "remove routes",
		"remove link ikeward0"}
	if !slices.Equal(f.kernel.changes, wantChanges) {
		t.Errorf("uninstall did %q to the kernel, want %q", f.kernel.changes, wantChanges)
	}
	for _, want := range []string{"Removed table ip ikeward\n",
		"Removed rule 32001: from all lookup 220 suppress_prefixlength 0\n",
		"Removed the routes of table 220: 2\n",
		"Removed /etc/swanctl/private/client-key.pem\n", "Removed link ikeward0\n"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("uninstall printed:\n%s\nwithout %q", f.out.String(), want)
		}
	}
}

// install refuses to run as any other program than files.Binary, which the units run, before it
// reads the profile.
func TestInstallElsewhere(t *testing.T) {
	for _, test := range []struct {
		setUp func(*fakeSystem)
		want  string
	}{
		{func(f *fakeSystem) { f.program = "/root/ikeward" },
			"install must run as /usr/local/sbin/ikeward, which the watchdog's service, the " +
				"routing's and strongSwan run, not as /root/ikeward"},
		{func(f *fakeSystem) {
			f.runsAs = func(string) (string, bool, error) {
				return "", false, errors.New("permission denied")
			}
		}, "cannot tell whether ikeward runs as /usr/local/sbin/ikeward: permission denied"},
	} {
		f := newFakeSystem(t, "secret\n")
		test.setUp(f)
		err := run(installArgs(writeProfile(t, "secret")), f.system)
		if err == nil || err.Error() != test.want || exitStatus(err) != 1 {
			t.Errorf("%v, want %s", err, test.want)
		}
		if got := f.installed(t); len(got) > 0 || f.out.Len() > 0 {
			t.Errorf("wrote %q, and printed %q", got, f.out.String())
		}
	}
}

// Where ikeward does not run as files.Binary, a dry run says that install would refuse to.
func TestDryRunElsewhere(t *testing.T) {
	f := newFakeSystem(t, "")
	f.program = "/root/ikeward"
	err := run(installArgs(writeProfile(t, ""), "--dry-run"), f.system)
	if err != nil {
		t.Fatal(err)
	}
	want := "Note: install must run as /usr/local/sbin/ikeward, which the watchdog's service, " +
		"the routing's and strongSwan run, not as /root/ikeward\nThe profile gives:\n"
	if got := f.out.String(); !strings.HasPrefix(got, want) {
		t.Errorf("printed:\n%s\nwant it to start with:\n%s", got, want)
	}
	f.out.Reset()
	f.stdout = failingWriter{}
	err = run(installArgs(writeProfile(t, ""), "--dry-run"), f.system)
	if err == nil || err.Error() != "the pipe is closed" {
		t.Errorf("a dry run with a failing output: %v", err)
	}
}

// A dry run of either writes nothing.
func TestDryRunCommand(t *testing.T) {
	f := newFakeSystem(t, "")
	err := run(installArgs(writeProfile(t, ""), "--dry-run"), f.system)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.installed(t); len(got) > 0 {
		t.Errorf("a dry run of install wrote %q", got)
	}
	if !strings.Contains(f.out.String(), "Would create /etc/swanctl/conf.d/ikeward.conf") {
		t.Errorf("install printed:\n%s", f.out.String())
	}

	if err := run(installArgs(writeProfile(t, "")), f.system); err != nil {
		t.Fatal(err)
	}
	f.out.Reset()
	if err := run([]string{"uninstall", "--dry-run"}, f.system); err != nil {
		t.Fatal(err)
	}
	if got := f.installed(t); !slices.Equal(got, allFiles) || len(f.nat.calls) > 0 ||
		len(f.kernel.changes) > 0 {
		t.Errorf("a dry run of uninstall left %q, want %q, and did %q to the tables and %q to "+
			"the kernel", got, allFiles, f.nat.calls, f.kernel.changes)
	}
	for _, want := range []string{"Would remove table ip ikeward\n",
		"Would remove rule 32000: from all lookup main suppress_prefixlength 0\n",
		"Would remove the routes of table 220: 2\n",
		"Would remove " + install.Record + "\n", "Would remove link ikeward0\n"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("uninstall printed:\n%s\nwithout %q", f.out.String(), want)
		}
	}
}

func TestInstallCommandErrors(t *testing.T) {
	secret := writeProfile(t, "secret")
	for _, test := range []struct {
		name     string
		args     []string
		setUp    func(*fakeSystem)
		want     string
		terminal bool
	}{
		{"not root", installArgs(secret), func(f *fakeSystem) { f.uid = 1000 },
			"install changes the system's settings, and must run as root", false},
		{"uninstall not root", []string{"uninstall", "--dry-run"},
			func(f *fakeSystem) { f.uid = 1000 },
			"uninstall changes the system's settings, and must run as root", false},
		{"wrong password", installArgs(secret),
			func(f *fakeSystem) { f.stdin = strings.NewReader("wrong\n") },
			"client.p12: the password is incorrect", false},
		{"empty input", installArgs(secret),
			func(f *fakeSystem) { f.stdin = strings.NewReader("") },
			"client.p12: cannot read its password: standard input, not a terminal, is empty", false},
		{"terminal", installArgs(secret), func(f *fakeSystem) {
			f.term = fakeTerminal{terminal: true, err: errors.New("interrupted")}
		}, "client.p12: cannot read its password: interrupted", true},
		{"prompt", installArgs(secret), func(f *fakeSystem) {
			f.term = fakeTerminal{terminal: true, password: "secret"}
			f.stderr = failingWriter{}
		}, "client.p12: cannot read its password: the pipe is closed", false},
		{"after the prompt", installArgs(secret), func(f *fakeSystem) {
			f.term = fakeTerminal{terminal: true, password: "secret"}
			f.stderr = &failAfter{}
		}, "client.p12: cannot read its password: the pipe is closed", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFakeSystem(t, "secret\n")
			if test.setUp != nil {
				test.setUp(f)
			}
			err := run(test.args, f.system)
			if err == nil || err.Error() != test.want {
				t.Errorf("%v, want %s", err, test.want)
			}
			if got := exitStatus(err); got != 1 {
				t.Errorf("exit status %d, want 1", got)
			}
			if got := f.installed(t); len(got) > 0 {
				t.Errorf("wrote %q", got)
			}
			if test.terminal != (f.term.reads > 0) {
				t.Errorf("read the terminal %d times", f.term.reads)
			}
		})
	}
}

// install names the flag whose file it cannot read, reads no file past profile.MaxSize, and
// refuses a .mobileconfig named apart from the .p12, writing nothing.
func TestInstallProfileErrors(t *testing.T) {
	dir, missing := writeProfile(t, ""), t.TempDir()
	other := filepath.Join(t.TempDir(), "other.mobileconfig")
	if err := os.WriteFile(other, []byte("<plist/>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	noFile := func(flag, name string) string {
		return "cannot read " + flag + ": open " + filepath.Join(missing, name) +
			": no such file or directory"
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{installArgs(missing), noFile("--p12", "client.p12")},
		{installArgs(dir, "--sswan", filepath.Join(missing, "client.sswan")),
			noFile("--sswan", "client.sswan")},
		{installArgs(dir, "--mobileconfig", filepath.Join(missing, "client.mobileconfig")),
			noFile("--mobileconfig", "client.mobileconfig")},
		{installArgs(dir, "--mobileconfig", other),
			"client.p12 and other.mobileconfig are not named alike, as one profile's files are"},
		{installArgs(dir, "--sswan", "/dev/zero"),
			"zero: over 1048576 bytes, more than an issued file holds"},
	} {
		f := newFakeSystem(t, "")
		err := run(test.args, f.system)
		if err == nil || err.Error() != test.want || exitStatus(err) != 1 {
			t.Errorf("%q: %v, want %s, and exit status 1", test.args, err, test.want)
		}
		if got := f.installed(t); len(got) > 0 {
			t.Errorf("%q: wrote %q", test.args, got)
		}
	}
}
