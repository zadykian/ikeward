package install

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// restartBoth are the commands of an install that restarts networkd and strongSwan.
var restartBoth = []string{
	"systemctl daemon-reload",
	"systemctl restart systemd-networkd.service",
	"systemctl enable ikeward-routing.service strongswan.service ikeward-watchdog.timer",
	"systemctl restart ikeward-routing.service",
	"systemctl restart strongswan.service",
	"swanctl --load-all --noprompt",
	"systemctl start ikeward-watchdog.timer",
}

// Where install stops half way, the record already lists the files install was to write, for
// uninstall.
func TestInstallHalfWay(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	paths := pathsOf(want(t, p))
	f.failWrite[paths[3]] = errors.New("no space left on device")
	_, err := onHome(f, r, &bytes.Buffer{}).Install(t.Context(), p, settings)
	if want := "cannot write " + paths[3] + ": no space left on device"; err == nil ||
		err.Error() != want {
		t.Fatalf("Install: %v, want %s", err, want)
	}
	if got, want := f.files[Record].data, recordOf(paths...); !bytes.Equal(got, want) {
		t.Errorf("the record is\n%s\nwant\n%s", got, want)
	}
	if len(r.ran()) > 0 {
		t.Errorf("ran %q", r.ran())
	}
}

// Where install stops half way, Journal lists the steps it owes, and a rerun takes them all,
// though the files are then as they should be. Journal goes as the rerun ends. Pending, from
// v0.2.0, owes every step, and Journal keeps them too.
func TestInstallRerun(t *testing.T) {
	p := newProfile(t, "client")
	for _, test := range []struct {
		name  string
		setUp func(*testing.T, *fakeFiles, *fakeRunner)
	}{
		{"no space", func(_ *testing.T, f *fakeFiles, _ *fakeRunner) {
			f.failWrite["/etc/swanctl/private/client-key.pem"] = errors.New("no space left on device")
		}},
		{"daemon-reload", func(_ *testing.T, _ *fakeFiles, r *fakeRunner) {
			r.fail["systemctl daemon-reload"] = errors.New("exit status 1")
		}},
		{"strongSwan's restart", func(_ *testing.T, _ *fakeFiles, r *fakeRunner) {
			r.fail["systemctl restart strongswan.service"] = errors.New("exit status 1")
		}},
		{"a new drop-in, networkd pending", func(t *testing.T, f *fakeFiles, r *fakeRunner) {
			t.Helper()
			installOn(t, f, r, p)
			f.files["/etc/strongswan.d/zz-ikeward.conf"] = file{[]byte("# An older drop-in\n"), 0o644}
			f.files[Pending] = file{pendingOf(networkd), 0o644}
			r.fail["systemctl restart strongswan.service"] = errors.New("exit status 1")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, r := newFakeFiles(), newFakeRunner(Packages...)
			test.setUp(t, f, r)
			if _, err := onHome(f, r, &bytes.Buffer{}).Install(t.Context(), p, settings); err == nil {
				t.Fatal("Install did not fail")
			}
			if got := f.files[Journal].data; !bytes.Equal(got, journalOf(restartBoth...)) {
				t.Errorf("Journal is\n%s", got)
			}
			f.failWrite, r.fail, r.commands = map[string]error{}, map[string]error{}, nil
			var out bytes.Buffer
			if _, err := onHome(f, r, &out).Install(t.Context(), p, settings); err != nil {
				t.Fatal(err)
			}
			checkLines(t, "the commands", r.ran(), restartBoth)
			for _, name := range []string{Journal, Pending} {
				if _, left := f.files[name]; left {
					t.Errorf("%s is left", name)
				}
			}
			note := "Running systemctl restart strongswan.service, as " + owedWhy + "\n"
			if !strings.Contains(out.String(), note) {
				t.Errorf("the output lacks %q:\n%s", note, out.String())
			}
		})
	}
}

// An older version's drop-ins of networkd and strongSwan, which the record lists, go as install
// runs. Where install stops after that, a rerun finds them gone, and restarts both daemons all the
// same, as Journal lists them.
func TestInstallRerunStale(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	installOn(t, f, r, p)
	old := []string{"/etc/systemd/networkd.conf.d/old.conf", "/etc/strongswan.d/old.conf"}
	for _, path := range old {
		f.files[path] = file{[]byte("# An old drop-in\n"), 0o644}
	}
	f.files[Record] = file{recordOf(append(pathsOf(want(t, p)), old...)...), 0o644}
	restarts := []string{"systemctl restart systemd-networkd.service",
		"systemctl restart strongswan.service"}
	r.fail[restarts[0]] = errors.New("exit status 1")
	_, err := onHome(f, r, &bytes.Buffer{}).Install(t.Context(), p, settings)
	if want := restarts[0] + ": exit status 1"; err == nil || err.Error() != want {
		t.Fatalf("Install: %v, want %s", err, want)
	}
	checkLines(t, "the files removed", f.removes, old)
	if got := f.files[Journal].data; !bytes.Equal(got, journalOf(restarts...)) {
		t.Errorf("Journal is\n%s", got)
	}

	f.removes, r.fail, r.commands = nil, map[string]error{}, nil
	if _, err := onHome(f, r, &bytes.Buffer{}).Install(t.Context(), p, settings); err != nil {
		t.Fatal(err)
	}
	checkLines(t, "the files removed", f.removes, []string{Journal})
	checkLines(t, "the commands", r.ran(), restarts)
}
