package install

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// The connection's file and the watchdog's timer, which the tests change.
const (
	connection = "/etc/swanctl/conf.d/ikeward.conf"
	timer      = "/etc/systemd/system/ikeward-watchdog.timer"
)

// On a system without the VPN, install installs the missing packages, writes every file and
// the record, then restarts networkd and strongSwan, whose settings are new. Pending lists them
// from before their settings are written until install ends.
func TestInstall(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner("strongswan", "strongswan-swanctl")
	var out bytes.Buffer
	if err := onHome(f, r, &out).Install(p, settings); err != nil {
		t.Fatal(err)
	}

	files := want(t, p)
	paths := pathsOf(files)
	checkLines(t, "the files written", f.writes, append([]string{Record, Pending}, paths...))
	checkLines(t, "the files removed", f.removes, []string{Pending})
	for _, want := range files {
		got := f.files[want.Path]
		if !bytes.Equal(got.data, want.Data) || got.mode != want.Mode {
			t.Errorf("%s: mode %04o and\n%s\nwant mode %04o and\n%s", want.Path, got.mode,
				got.data, want.Mode, want.Data)
		}
	}
	if got := f.files["/etc/swanctl/private/client-key.pem"].mode; got != 0o600 {
		t.Errorf("the key's mode is %04o, want 0600", got)
	}
	if got, want := f.files[Record].data, recordOf(paths...); !bytes.Equal(got, want) {
		t.Errorf("the record is\n%s\nwant\n%s", got, want)
	}

	checkLines(t, "the commands", r.commands, []string{
		"dpkg-query --show --showformat=${db:Status-Status} strongswan",
		"dpkg-query --show --showformat=${db:Status-Status} strongswan-swanctl",
		"dpkg-query --show --showformat=${db:Status-Status} charon-systemd",
		"dpkg-query --show --showformat=${db:Status-Status} libcharon-extra-plugins",
		"apt-get install --yes -o DPkg::Lock::Timeout=300 charon-systemd libcharon-extra-plugins",
		"systemctl daemon-reload",
		"systemctl restart systemd-networkd.service",
		"systemctl enable ikeward-routing.service strongswan.service ikeward-watchdog.timer",
		"systemctl restart ikeward-routing.service",
		"systemctl restart strongswan.service",
		"swanctl --load-all --noprompt",
		"systemctl start ikeward-watchdog.timer",
	})
	var wantOut []string
	for _, line := range r.ran() {
		if strings.HasPrefix(line, "apt-get") {
			wantOut = append(wantOut, "Running "+line)
		}
	}
	for _, path := range paths {
		wantOut = append(wantOut, "Created "+path)
	}
	for _, line := range r.ran()[1:] {
		wantOut = append(wantOut, "Running "+line)
	}
	checkLines(t, "the output", outputLines(t, out.String()), wantOut)
	if !strings.Contains(out.String(), "  "+p.Summary()[0]+"\n") {
		t.Errorf("the output lacks the profile's summary:\n%s", out.String())
	}
}

// Run again, install writes nothing and installs nothing, but reloads, loads and enables all
// the same, which mends an install that stopped half way. Without Pending, it restarts nothing.
func TestInstallAgain(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	installOn(t, f, r, p)
	var out bytes.Buffer
	if err := onHome(f, r, &out).Install(p, settings); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) > 0 || len(f.removes) > 0 {
		t.Errorf("wrote %q and removed %q", f.writes, f.removes)
	}
	commands := []string{
		"systemctl daemon-reload",
		"networkctl reload",
		"systemctl enable ikeward-routing.service strongswan.service ikeward-watchdog.timer",
		"systemctl restart ikeward-routing.service",
		"systemctl start strongswan.service",
		"swanctl --load-all --noprompt",
		"systemctl start ikeward-watchdog.timer",
	}
	checkLines(t, "the commands", r.ran(), commands)
	wantOut := []string{"Left 12 of the 12 files as they were"}
	for _, line := range commands {
		wantOut = append(wantOut, "Running "+line)
	}
	checkLines(t, "the output", outputLines(t, out.String()), wantOut)
}

// install writes only the files that differ, in content or in mode, and restarts no daemon whose
// settings stay.
func TestInstallChanges(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	if err := onHome(f, r, &bytes.Buffer{}).Install(p, settings); err != nil {
		t.Fatal(err)
	}
	key := "/etc/swanctl/private/client-key.pem"
	f.files[connection] = file{[]byte("connections {}\n"), 0o644}
	f.files[key] = file{f.files[key].data, 0o644}
	delete(f.files, timer)
	f.writes, r.commands = nil, nil

	var out bytes.Buffer
	if err := onHome(f, r, &out).Install(p, settings); err != nil {
		t.Fatal(err)
	}
	checkLines(t, "the files written", f.writes, []string{connection, timer, key})
	if got := f.files[key].mode; got != 0o600 {
		t.Errorf("the key's mode is %04o, want 0600", got)
	}
	got := outputLines(t, out.String())
	checkLines(t, "the output", got[:4], []string{"Updated " + connection, "Created " + timer,
		"Updated " + key, "Left 9 of the 12 files as they were"})
	for _, line := range r.ran() {
		if restartsDaemon(line) {
			t.Errorf("ran %s", line)
		}
	}
}

// A change to networkd's or strongSwan's own settings restarts that daemon alone.
func TestTakeUp(t *testing.T) {
	for _, test := range []struct {
		changed  []string
		networkd string
		swan     string
	}{
		{nil, "networkctl reload", "systemctl start strongswan.service"},
		{[]string{"/etc/systemd/network/25-ikeward0.network", "/etc/swanctl/conf.d/ikeward.conf",
			"/etc/systemd/system/ikeward-watchdog.service"},
			"networkctl reload", "systemctl start strongswan.service"},
		{[]string{"/etc/systemd/networkd.conf.d/10-keep-vpn-routing.conf"},
			"systemctl restart systemd-networkd.service", "systemctl start strongswan.service"},
		{[]string{"/etc/strongswan.d/zz-ikeward.conf"},
			"networkctl reload", "systemctl restart strongswan.service"},
	} {
		restart := &restarts{}
		if err := (System{DryRun: true}).pend(restart, test.changed); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, command := range takeUp(restart.units) {
			got = append(got, strings.Join(command, " "))
		}
		checkLines(t, strings.Join(test.changed, ", "), got, []string{
			"systemctl daemon-reload",
			test.networkd,
			"systemctl enable ikeward-routing.service strongswan.service ikeward-watchdog.timer",
			"systemctl restart ikeward-routing.service",
			test.swan,
			"swanctl --load-all --noprompt",
			"systemctl start ikeward-watchdog.timer",
		})
	}
}

// A profile of another name replaces the certificate and key of the one before, and install
// removes those; the record lists them until they are gone.
func TestInstallStale(t *testing.T) {
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	installOn(t, f, r, newProfile(t, "before"))
	p := newProfile(t, "after")
	var out bytes.Buffer
	if err := onHome(f, r, &out).Install(p, settings); err != nil {
		t.Fatal(err)
	}
	stale := []string{"/etc/swanctl/x509/before-cert.pem", "/etc/swanctl/private/before-key.pem"}
	checkLines(t, "the files removed", f.removes, stale)
	paths := pathsOf(want(t, p))
	// The connection names the certificate.
	checkLines(t, "the files written", f.writes, []string{Record, connection,
		"/etc/swanctl/x509/after-cert.pem", "/etc/swanctl/x509ca/ikeward-ca.pem",
		"/etc/swanctl/private/after-key.pem", Record})
	checkLines(t, "the files", f.paths(), slices.Sorted(slices.Values(append(paths, Record))))
	if got, want := f.files[Record].data, recordOf(paths...); !bytes.Equal(got, want) {
		t.Errorf("the record is\n%s\nwant\n%s", got, want)
	}
	got := outputLines(t, out.String())
	checkLines(t, "the output", got[:7], []string{
		"Updated " + connection,
		"Created /etc/swanctl/x509/after-cert.pem",
		"Updated /etc/swanctl/x509ca/ikeward-ca.pem",
		"Created /etc/swanctl/private/after-key.pem",
		"Left 8 of the 12 files as they were",
		"Removed " + stale[0],
		"Removed " + stale[1],
	})
}
