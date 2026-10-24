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
// the record, then takes every step, as each reads a file that is new. networkd and strongSwan
// restart, whose settings are new. Journal lists the steps from before a file is written until
// install ends.
func TestInstall(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner("strongswan", "strongswan-swanctl")
	var out bytes.Buffer
	if err := onHome(f, r, &out).Install(t.Context(), p, settings); err != nil {
		t.Fatal(err)
	}

	files := want(t, p)
	paths := pathsOf(files)
	checkLines(t, "the files written", f.writes, append([]string{Journal, Record}, paths...))
	checkLines(t, "the files removed", f.removes, []string{Journal})
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
	wantOut := []string{"Running " + r.ran()[0]}
	for _, path := range paths {
		wantOut = append(wantOut, "Created "+path)
	}
	wantOut = append(wantOut, "Created "+Record)
	for i, line := range r.ran()[1:] {
		wantOut = append(wantOut, "Running "+line+", as "+[]string{
			"/etc/systemd/system/ikeward-watchdog.service", "/etc/systemd/networkd.conf.d/" +
				"10-keep-vpn-routing.conf", timer, "/etc/systemd/system/ikeward-routing.service",
			"/etc/strongswan.d/zz-ikeward.conf", connection, timer}[i]+" changes")
	}
	checkLines(t, "the output", outputLines(t, out.String()), wantOut)
	if !strings.Contains(out.String(), "  "+p.Summary()[0]+"\n") {
		t.Errorf("the output lacks the profile's summary:\n%s", out.String())
	}
}

// Run again on the system it set up, install writes nothing and installs nothing. It takes no
// step that a probe finds not due, but those with no probe run all the same.
func TestInstallAgain(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	installOn(t, f, r, p)
	var out bytes.Buffer
	if err := onHome(f, r, &out).Install(t.Context(), p, settings); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) > 0 || len(f.removes) > 0 {
		t.Errorf("wrote %q and removed %q", f.writes, f.removes)
	}
	commands := []string{
		"systemctl restart ikeward-routing.service",
		"swanctl --load-all --noprompt",
	}
	checkLines(t, "the commands", r.ran(), commands)
	wantOut := []string{"Left 12 of the 12 files as they were"}
	for _, line := range commands {
		wantOut = append(wantOut, "Running "+line)
	}
	checkLines(t, "the output", outputLines(t, out.String()), wantOut)
}

// install writes only the files that differ, in content or in mode, and names the file that
// makes each step due. It restarts no daemon whose settings stay.
func TestInstallChanges(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	if err := onHome(f, r, &bytes.Buffer{}).Install(t.Context(), p, settings); err != nil {
		t.Fatal(err)
	}
	key := "/etc/swanctl/private/client-key.pem"
	f.files[connection] = file{[]byte("connections {}\n"), 0o644}
	f.files[key] = file{f.files[key].data, 0o644}
	delete(f.files, timer)
	f.writes, r.commands = nil, nil

	var out bytes.Buffer
	if err := onHome(f, r, &out).Install(t.Context(), p, settings); err != nil {
		t.Fatal(err)
	}
	checkLines(t, "the files written", f.writes, []string{Journal, connection, timer, key})
	if got := f.files[key].mode; got != 0o600 {
		t.Errorf("the key's mode is %04o, want 0600", got)
	}
	checkLines(t, "the output", outputLines(t, out.String()), []string{
		"Updated " + connection, "Created " + timer, "Updated " + key,
		"Left 9 of the 12 files as they were",
		"Running systemctl daemon-reload, as " + timer + " changes",
		"Running systemctl enable ikeward-routing.service strongswan.service " +
			"ikeward-watchdog.timer, as " + timer + " changes",
		"Running systemctl restart ikeward-routing.service",
		"Running swanctl --load-all --noprompt, as " + connection + " changes",
		"Running systemctl start ikeward-watchdog.timer, as " + timer + " changes",
	})
}

// A profile of another name replaces the certificate and key of the one before, and install
// removes those; the record lists them until they are gone.
func TestInstallStale(t *testing.T) {
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	installOn(t, f, r, newProfile(t, "before"))
	p := newProfile(t, "after")
	var out bytes.Buffer
	if err := onHome(f, r, &out).Install(t.Context(), p, settings); err != nil {
		t.Fatal(err)
	}
	stale := []string{"/etc/swanctl/x509/before-cert.pem", "/etc/swanctl/private/before-key.pem"}
	checkLines(t, "the files removed", f.removes, append(stale, Journal))
	paths := pathsOf(want(t, p))
	// The connection names the certificate.
	checkLines(t, "the files written", f.writes, []string{Journal, Record, connection,
		"/etc/swanctl/x509/after-cert.pem", "/etc/swanctl/x509ca/ikeward-ca.pem",
		"/etc/swanctl/private/after-key.pem", Record})
	checkLines(t, "the files", f.paths(), slices.Sorted(slices.Values(append(paths, Record))))
	if got, want := f.files[Record].data, recordOf(paths...); !bytes.Equal(got, want) {
		t.Errorf("the record is\n%s\nwant\n%s", got, want)
	}
	got := outputLines(t, out.String())
	checkLines(t, "the output", got[:8], []string{
		"Updated " + connection,
		"Created /etc/swanctl/x509/after-cert.pem",
		"Updated /etc/swanctl/x509ca/ikeward-ca.pem",
		"Created /etc/swanctl/private/after-key.pem",
		"Left 8 of the 12 files as they were",
		"Removed " + stale[0],
		"Removed " + stale[1],
		"Updated " + Record,
	})
}
