package install

import (
	"bytes"
	"strings"
	"testing"
)

// A dry run changes nothing, runs nothing but dpkg-query, and prints the files as diffs, but
// for the key. It says why each step is due.
func TestInstallDryRun(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages[1:]...)
	var out bytes.Buffer
	if err := dryRunOn(f, r, &out).Install(p, settings); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) > 0 || len(r.ran()) > 0 {
		t.Errorf("wrote %q and ran %q", f.writes, r.ran())
	}
	got := out.String()
	files := want(t, p)
	key := files[len(files)-1]
	for _, want := range []string{
		"Would run apt-get install --yes -o DPkg::Lock::Timeout=300 strongswan\n",
		"Would create /etc/strongswan.d/zz-ikeward.conf, mode 0644:\n" +
			"--- /dev/null\n+++ /etc/strongswan.d/zz-ikeward.conf\n@@ -0,0 +1,30 @@\n" +
			"+# Written by ikeward install",
		"Would create /etc/swanctl/private/client-key.pem, mode 0600, which only root may read: " +
			"not shown\n",
		"Would create " + Record + "\n",
		"Would run systemctl restart strongswan.service, as /etc/strongswan.d/zz-ikeward.conf " +
			"changes\n",
		"Would run systemctl start ikeward-watchdog.timer, as " + timer + " changes\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "PRIVATE KEY") || strings.Contains(got, string(key.Data[30:60])) {
		t.Errorf("the output shows the key:\n%s", got)
	}
}

// On a system with the VPN installed, a dry run shows what differs alone, and the step that reads
// it.
func TestInstallDryRunChanges(t *testing.T) {
	p := newProfile(t, "client")
	f := newFakeFiles()
	if err := onHome(f, newFakeRunner(Packages...), &bytes.Buffer{}).Install(p,
		settings); err != nil {
		t.Fatal(err)
	}
	key := "/etc/swanctl/private/client-key.pem"
	old := f.files[connection].data
	f.files[connection] = file{bytes.Replace(old, []byte("dpd_delay    = 30s"),
		[]byte("dpd_delay    = 10s"), 1), 0o640}
	f.files[key] = file{[]byte("another key\n"), 0o600}
	f.writes = nil
	var out bytes.Buffer
	r := newFakeRunner(Packages...)
	if err := dryRunOn(f, r, &out).Install(p, settings); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) > 0 || len(r.ran()) > 0 {
		t.Errorf("wrote %q and ran %q", f.writes, r.ran())
	}
	checkLines(t, "the output", outputLines(t, out.String()), []string{
		"Would update /etc/swanctl/conf.d/ikeward.conf, mode 0640 to 0644:",
		"--- /etc/swanctl/conf.d/ikeward.conf",
		"+++ /etc/swanctl/conf.d/ikeward.conf",
		"@@ -8,7 +8,7 @@",
		"         vips         = 0.0.0.0",
		"         # A reconnect replaces the old SA rather than leave it beside the new.",
		"         unique       = replace",
		"-        dpd_delay    = 10s",
		"+        dpd_delay    = 30s",
		"         # charon tries again each time the server leaves an attempt unanswered,",
		"         # until the tunnel connects or an error ends it for good, as a refused",
		"         # login. swanctl's default, 1, gives up after one: 165 s of retransmits.",
		"Would update /etc/swanctl/private/client-key.pem, which only root may read: not shown",
		"Left 10 of the 12 files as they were",
		"Would run systemctl daemon-reload",
		"Would run networkctl reload",
		"Would run systemctl enable ikeward-routing.service strongswan.service ikeward-watchdog.timer",
		"Would run systemctl restart ikeward-routing.service",
		"Would run systemctl start strongswan.service",
		"Would run swanctl --load-all --noprompt, as " + connection + " changes",
		"Would run systemctl start ikeward-watchdog.timer",
	})
}

// A dry run says which files of an earlier profile it would remove, and a change of mode alone.
// The record would change, and the steps that read those files are due.
func TestInstallDryRunStale(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	installOn(t, f, r, p)
	f.files[Record] = file{append(f.files[Record].data, "/etc/swanctl/x509/old-cert.pem\n"...),
		0o644}
	f.files[timer] = file{f.files[timer].data, 0o600}
	var out bytes.Buffer
	if err := dryRunOn(f, r, &out).Install(p, settings); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) > 0 || len(f.removes) > 0 || len(r.ran()) > 0 {
		t.Errorf("wrote %q, removed %q and ran %q", f.writes, f.removes, r.ran())
	}
	checkLines(t, "the output", outputLines(t, out.String()), []string{
		"Would update " + timer + ", mode 0600 to 0644",
		"Left 11 of the 12 files as they were",
		"Would remove /etc/swanctl/x509/old-cert.pem",
		"Would update " + Record,
		"Would run systemctl daemon-reload, as " + timer + " changes",
		"Would run networkctl reload",
		"Would run systemctl enable ikeward-routing.service strongswan.service " +
			"ikeward-watchdog.timer, as " + timer + " changes",
		"Would run systemctl restart ikeward-routing.service",
		"Would run systemctl start strongswan.service",
		"Would run swanctl --load-all --noprompt, as /etc/swanctl/x509/old-cert.pem changes",
		"Would run systemctl start ikeward-watchdog.timer, as " + timer + " changes",
	})
}

// Where Pending lists a daemon, a dry run says it would restart it, as an earlier install owes
// it, though its settings are as they should be. Pending stays.
func TestInstallDryRunPending(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	installOn(t, f, r, p)
	f.files[Pending] = file{pendingOf(StrongSwan), 0o644}
	var out bytes.Buffer
	if err := dryRunOn(f, r, &out).Install(p, settings); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) > 0 || len(f.removes) > 0 || len(r.ran()) > 0 {
		t.Errorf("wrote %q, removed %q and ran %q", f.writes, f.removes, r.ran())
	}
	if got := f.files[Pending].data; !bytes.Equal(got, pendingOf(StrongSwan)) {
		t.Errorf("Pending is\n%s", got)
	}
	checkLines(t, "the output", outputLines(t, out.String()), []string{
		"Left 12 of the 12 files as they were",
		"Would run systemctl daemon-reload",
		"Would run networkctl reload",
		"Would run systemctl enable ikeward-routing.service strongswan.service ikeward-watchdog.timer",
		"Would run systemctl restart ikeward-routing.service",
		"Would run systemctl restart strongswan.service, as " + owedWhy,
		"Would run swanctl --load-all --noprompt",
		"Would run systemctl start ikeward-watchdog.timer",
	})
}

// A dry run shows no content of a file that only root may read on the system, though install
// would let others read it.
func TestInstallDryRunWasSecret(t *testing.T) {
	p := newProfile(t, "client")
	f := newFakeFiles()
	f.files[connection] = file{[]byte("secrets { ike { secret = by-hand } }\n"), 0o600}
	var out bytes.Buffer
	err := dryRunOn(f, newFakeRunner(Packages...), &out).Install(p, settings)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	want := "Would update " + connection + ", mode 0600 to 0644, which only root may read: " +
		"not shown\n"
	if !strings.Contains(got, want) || strings.Contains(got, "by-hand") {
		t.Errorf("the output lacks %q, or shows the old file:\n%s", want, got)
	}
}
