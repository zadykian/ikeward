package install

import (
	"bytes"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/profile"
)

func TestInstallErrors(t *testing.T) {
	p := newProfile(t, "client")
	paths := pathsOf(want(t, p))
	failed := errors.New("failed")
	for _, test := range []struct {
		name  string
		p     *profile.Profile
		setUp func(*fakeFiles, *fakeRunner)
		want  string
	}{
		{"no profile", nil, nil, "cannot generate the files: no profile"},
		{"no key", &profile.Profile{Name: "client", Server: "vpn.example.org", Port: 500,
			RemoteID: "vpn.example.org", IKEProposals: "aes256-sha256-modp2048",
			ESPProposals: "aes128gcm16"}, nil,
			"cannot write the credentials: the profile lacks them"},
		{"apt-get", p, func(_ *fakeFiles, r *fakeRunner) {
			r.installed = map[string]bool{}
			r.fail["apt-get install --yes -o DPkg::Lock::Timeout=300 "+
				strings.Join(Packages, " ")] = errors.New("exit status 100: E: no space")
		}, "apt-get install --yes -o DPkg::Lock::Timeout=300 " + strings.Join(Packages, " ") +
			": exit status 100: E: no space"},
		{"record unread", p, func(f *fakeFiles, _ *fakeRunner) {
			f.failRead[Record] = fs.ErrPermission
		}, "cannot read " + Record + ": permission denied"},
		{"record wrong", p, func(f *fakeFiles, _ *fakeRunner) {
			f.files[Record] = file{[]byte("# comment\n\n/etc/x\netc/y\n"), 0o644}
		}, Record + `: line 4: "etc/y" is not a clean, absolute path`},
		{"record unclean", p, func(f *fakeFiles, _ *fakeRunner) {
			f.files[Record] = file{[]byte("/etc/../x\n"), 0o644}
		}, Record + `: line 1: "/etc/../x" is not a clean, absolute path`},
		{"record unwritten", p, func(f *fakeFiles, _ *fakeRunner) {
			f.failWrite[Record] = failed
		}, "cannot write " + Record + ": failed"},
		{"file unread", p, func(f *fakeFiles, _ *fakeRunner) {
			f.failRead[paths[1]] = fs.ErrPermission
		}, "cannot read " + paths[1] + ": permission denied"},
		{"stale file", p, func(f *fakeFiles, _ *fakeRunner) {
			f.files[Record] = file{[]byte("/etc/old\n"), 0o644}
			f.files["/etc/old"] = file{nil, 0o644}
			f.failRemove["/etc/old"] = failed
		}, "cannot remove /etc/old: failed"},
		{"command", p, func(_ *fakeFiles, r *fakeRunner) {
			r.fail["swanctl --load-all --noprompt"] = errors.New("exit status 2: no charon")
		}, "swanctl --load-all --noprompt: exit status 2: no charon"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, r := newFakeFiles(), newFakeRunner(Packages...)
			if test.setUp != nil {
				test.setUp(f, r)
			}
			err := onHome(f, r, &bytes.Buffer{}).Install(test.p, settings)
			if err == nil || err.Error() != test.want {
				t.Errorf("Install: %v, want %s", err, test.want)
			}
		})
	}
}

// install fails where it cannot read Journal or Pending, or write or remove Journal. Or where
// Journal lists other than a step's command, or Pending a unit but networkd's and strongSwan's.
func TestInstallJournalErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		setUp func(*fakeFiles)
		want  string
	}{
		{"unread", func(f *fakeFiles) { f.failRead[Journal] = fs.ErrPermission },
			"cannot read " + Journal + ": permission denied"},
		{"wrong", func(f *fakeFiles) {
			f.files[Journal] = file{[]byte("# comment\n\nnetworkctl reload\nreboot\n"), 0o644}
		}, Journal + `: line 4: "reboot" is not a command of install's steps`},
		{"unwritten", func(f *fakeFiles) { f.failWrite[Journal] = errors.New("failed") },
			"cannot write " + Journal + ": failed"},
		{"unremoved", func(f *fakeFiles) { f.failRemove[Journal] = fs.ErrPermission },
			"cannot remove " + Journal + ": permission denied"},
		{"empty, unremoved", func(f *fakeFiles) {
			f.files[Journal] = file{[]byte("# comment\n"), 0o644}
			f.failRemove[Journal] = fs.ErrPermission
		}, "cannot remove " + Journal + ": permission denied"},
		{"Pending unread", func(f *fakeFiles) { f.failRead[Pending] = fs.ErrPermission },
			"cannot read " + Pending + ": permission denied"},
		{"Pending wrong", func(f *fakeFiles) {
			f.files[Pending] = file{[]byte("# comment\n\nstrongswan.service\nssh.service\n"), 0o644}
		}, Pending + `: line 4: "ssh.service" is not a daemon install restarts`},
		{"Pending unremoved", func(f *fakeFiles) {
			f.files[Pending] = file{pendingOf(StrongSwan), 0o644}
			f.failRemove[Pending] = fs.ErrPermission
		}, "cannot remove " + Pending + ": permission denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFakeFiles()
			test.setUp(f)
			err := onHome(f, newFakeRunner(Packages...), &bytes.Buffer{}).Install(
				newProfile(t, "client"), settings)
			if err == nil || err.Error() != test.want {
				t.Errorf("Install: %v, want %s", err, test.want)
			}
		})
	}
}

// A failed write of the output fails install, but only once install is done.
func TestInstallOutputFails(t *testing.T) {
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	err := onHome(f, r, failingWriter{}).Install(newProfile(t, "client"), settings)
	if err == nil || err.Error() != "the pipe is closed" {
		t.Errorf("Install: %v, want the write's error", err)
	}
	if len(f.writes) != 14 || len(r.ran()) != 7 {
		t.Errorf("wrote %q and ran %q", f.writes, r.ran())
	}
}

// Settings that files.Settings.Check refuses fail install before it changes anything.
func TestInstallWrongSettings(t *testing.T) {
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	wrong := settings
	wrong.Domains = nil
	err := onHome(f, r, &bytes.Buffer{}).Install(newProfile(t, "client"), wrong)
	if err == nil || err.Error() != "cannot generate the files: no domain" {
		t.Errorf("Install: %v, want Check's error", err)
	}
	if len(f.writes) > 0 || len(r.commands) > 0 {
		t.Errorf("wrote %q and ran %q", f.writes, r.commands)
	}
}

// Where apt-get install fails, the package lists may be missing or out of date: install updates
// them and tries once more.
func TestInstallUpdatesLists(t *testing.T) {
	aptInstall := "apt-get install --yes -o DPkg::Lock::Timeout=300 charon-systemd"
	locate := errors.New("exit status 100: E: Unable to locate package charon-systemd")
	for _, test := range []struct {
		name  string
		setUp func(*fakeRunner)
		ran   []string
		want  string
	}{
		{"updated", func(r *fakeRunner) { r.failOnce[aptInstall] = locate },
			[]string{aptInstall, "apt-get update", aptInstall}, ""},
		{"still failing", func(r *fakeRunner) { r.fail[aptInstall] = locate },
			[]string{aptInstall, "apt-get update", aptInstall}, aptInstall + ": " + locate.Error()},
		{"no update", func(r *fakeRunner) {
			r.failOnce[aptInstall] = locate
			r.fail["apt-get update"] = errors.New("exit status 100: E: Could not get lock")
		}, []string{aptInstall, "apt-get update"},
			"apt-get update: exit status 100: E: Could not get lock"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFakeFiles()
			r := newFakeRunner("strongswan", "strongswan-swanctl", "libcharon-extra-plugins")
			test.setUp(r)
			var out bytes.Buffer
			err := onHome(f, r, &out).Install(newProfile(t, "client"), settings)
			if (err == nil) != (test.want == "") || (err != nil && err.Error() != test.want) {
				t.Fatalf("Install: %v, want %q", err, test.want)
			}
			ran := r.ran()
			checkLines(t, "the commands", ran[:min(len(ran), len(test.ran))], test.ran)
			got := outputLines(t, out.String())
			checkLines(t, "the output", got[:3], []string{"Running " + aptInstall,
				"Updating the package lists, as " + aptInstall + ": " + locate.Error(),
				"Running apt-get update"})
			if test.want != "" && len(f.writes) > 0 {
				t.Errorf("wrote %q", f.writes)
			}
		})
	}
}
