package install

import (
	"bytes"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/routing"
)

// installed is a system on which install has run for a profile issued for client.
func installed(t *testing.T) (*fakeFiles, []string) {
	t.Helper()
	p := newProfile(t, "client")
	f := newFakeFiles()
	_, err := onHome(f, newFakeRunner(Packages...), &bytes.Buffer{}).Install(t.Context(), p,
		settings)
	if err != nil {
		t.Fatal(err)
	}
	f.writes, f.removes = nil, nil
	return f, pathsOf(want(t, p))
}

// uninstall stops the watchdog's timer, then its service, then strongSwan, then the routing's
// service. It removes the NAT table, charon's rule and ikeward's, then table 220's routes, with the
// floors. Then go the files, the records of the last restart and the learned ranges, Pending and
// its own. It reloads systemd and networkd, and removes the tunnel's link last.
func TestUninstall(t *testing.T) {
	f, paths := installed(t)
	f.files["/etc/other.conf"] = file{[]byte("the user's\n"), 0o644}
	f.files[LastRestart] = file{[]byte("2026-10-05T12:00:00Z\n"), 0o644}
	f.files[Pending] = file{pendingOf(StrongSwan), 0o644}
	f.files[routing.Learned] = file{[]byte("192.0.2.0/24\n"), 0o644}
	// A file already gone is passed over.
	delete(f.files, paths[2])
	r := newFakeRunner()
	tables, network := withNAT(), home()
	// charon's rule, which a charon that died left, looks up table 220 too, and goes.
	charons := "220: from all lookup 220"
	network.rules = slices.Insert(network.rules, 1, routing.Rule{Priority: 220, Table: 220,
		SuppressPrefixlen: -1})
	var out bytes.Buffer
	err := System{Files: f, Runner: r, Tables: tables, Network: network, Out: &out}.Uninstall()
	if err != nil {
		t.Fatal(err)
	}
	checkLines(t, "the links removed", network.removed, []string{"ikeward0"})
	checkLines(t, "the tables removed", tables.removed, []string{"ikeward"})
	checkLines(t, "the rules removed", network.unruled, append([]string{charons}, ownRules...))
	if len(network.routes) > 0 {
		t.Errorf("left the routes %v", network.routes)
	}
	checkLines(t, "the files left", f.paths(), []string{"/etc/other.conf"})
	removed := append(append(append([]string{}, paths[:2]...), paths[3:]...), LastRestart, Pending,
		routing.Learned, Record)
	checkLines(t, "the files removed", f.removes, removed)
	checkLines(t, "the commands", r.ran(), []string{
		disable,
		disableRouting,
		"systemctl daemon-reload",
		"networkctl reload",
	})
	want := []string{"Running " + disable, "Running " + disableRouting,
		"Removed table ip ikeward", "Removed rule " + charons, "Removed rule " + ownRules[0],
		"Removed rule " + ownRules[1], "Removed the routes of table 220: 2"}
	for _, p := range removed {
		want = append(want, "Removed "+p)
	}
	want = append(want, "Running systemctl daemon-reload", "Running networkctl reload",
		"Removed link ikeward0")
	checkLines(t, "the output", lines(out.Bytes()), withNewlines(want))
}

// disable is how uninstall stops and disables the units, in the order they stop, and
// disableRouting how it then stops and disables the routing's.
const (
	disable = "systemctl disable --now ikeward-watchdog.timer ikeward-watchdog.service " +
		"strongswan.service"
	disableRouting = "systemctl disable --now ikeward-routing.service"
)

// ownRules are ikeward's rules, as ip rule lists them.
var ownRules = []string{"32000: from all lookup main suppress_prefixlength 0",
	"32001: from all lookup 220 suppress_prefixlength 0"}

// withNewlines is lines, each ended by a newline.
func withNewlines(lines []string) []string {
	var out []string
	for _, line := range lines {
		out = append(out, line+"\n")
	}
	return out
}

// Where systemd lacks a unit, uninstall disables the others alone, and removes the rest all the
// same: systemctl disables none where one is missing. Install may have stopped before it wrote
// the watchdog's units or the routing's, and strongSwan's packages may be gone.
func TestUninstallMissingUnits(t *testing.T) {
	for _, test := range []struct {
		name    string
		missing []string
		disable []string
	}{
		{"no watchdog", []string{Timer, Service},
			[]string{"systemctl disable --now strongswan.service", disableRouting}},
		{"no strongSwan", []string{StrongSwan},
			[]string{"systemctl disable --now ikeward-watchdog.timer ikeward-watchdog.service",
				disableRouting}},
		{"no routing", []string{"ikeward-routing.service"}, []string{disable}},
		{"none", []string{Timer, Service, StrongSwan, "ikeward-routing.service"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, paths := installed(t)
			delete(f.files, "/etc/systemd/system/ikeward-watchdog.timer")
			r := newFakeRunner()
			for _, unit := range test.missing {
				lacks(r, unit)
			}
			tables, network := withNAT(), home()
			if err := (System{Files: f, Runner: r, Tables: tables, Network: network,
				Out: &bytes.Buffer{}}).Uninstall(); err != nil {
				t.Fatal(err)
			}
			if len(f.files) > 0 || len(tables.removed) != 1 || len(network.unruled) != 2 {
				t.Errorf("left %q, and removed the tables %q and the rules %q", f.paths(),
					tables.removed, network.unruled)
			}
			if len(f.removes) != len(paths) {
				t.Errorf("removed %q", f.removes)
			}
			checkLines(t, "the commands", r.ran(), append(test.disable,
				"systemctl daemon-reload", "networkctl reload"))
		})
	}
}

// lacks has systemd, which r runs, lack unit, and fail a disable that names it, as systemd 259's
// systemctl does.
func lacks(r *fakeRunner, unit string) {
	r.noUnit[unit] = true
	for _, line := range []string{disable, disableRouting} {
		if strings.Contains(line, unit) {
			r.fail[line] = errors.New("exit status 1: Failed to disable unit: Unit " + unit +
				" does not exist")
		}
	}
}

// Where install has not run, or uninstall has already, uninstall does nothing.
func TestUninstallNothing(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		f, r, tables, network := newFakeFiles(), newFakeRunner(), withNAT(), home()
		var out bytes.Buffer
		if err := (System{Files: f, Runner: r, Tables: tables, Network: network,
			Out: &out, DryRun: dryRun}).Uninstall(); err != nil {
			t.Fatal(err)
		}
		if len(r.commands) > 0 || len(tables.removed) > 0 || len(network.unruled) > 0 ||
			len(network.removed) > 0 || len(network.routes) == 0 {
			t.Errorf("ran %q, and removed the tables %q, the rules %q, the links %q and the "+
				"routes", r.commands, tables.removed, network.unruled, network.removed)
		}
		if got, want := out.String(), "Nothing to remove: there is no "+Record+
			", which install writes\n"; got != want {
			t.Errorf("printed %q, want %q", got, want)
		}
	}
}

// A dry run names the table, the rules, the routes, the files and the link uninstall would
// remove, and removes none. The files include the watchdog's record of its last restart, Pending
// and the learned ranges, where they exist.
func TestUninstallDryRun(t *testing.T) {
	for _, records := range []bool{false, true} {
		f, paths := installed(t)
		if records {
			f.files[LastRestart] = file{[]byte("2026-10-05T12:00:00Z\n"), 0o644}
			f.files[Pending] = file{pendingOf(networkd), 0o644}
			f.files[routing.Learned] = file{[]byte("192.0.2.0/24\n"), 0o644}
			paths = append(paths, LastRestart, Pending, routing.Learned)
		}
		r := newFakeRunner()
		tables, network := withNAT(), home()
		var out bytes.Buffer
		if err := (System{Files: f, Runner: r, Tables: tables, Network: network, Out: &out,
			DryRun: true}).Uninstall(); err != nil {
			t.Fatal(err)
		}
		if len(f.removes) > 0 || len(r.ran()) > 0 || len(tables.removed) > 0 ||
			len(network.unruled) > 0 || len(network.removed) > 0 || len(network.routes) == 0 {
			t.Errorf("removed %q, the tables %q, the rules %q and the links %q, and ran %q",
				f.removes, tables.removed, network.unruled, network.removed, r.ran())
		}
		want := []string{"Would run " + disable, "Would run " + disableRouting,
			"Would remove table ip ikeward", "Would remove rule " + ownRules[0],
			"Would remove rule " + ownRules[1], "Would remove the routes of table 220: 2"}
		for _, p := range append(paths, Record) {
			want = append(want, "Would remove "+p)
		}
		want = append(want, "Would run systemctl daemon-reload", "Would run networkctl reload",
			"Would remove link ikeward0")
		checkLines(t, "the output", lines(out.Bytes()), withNewlines(want))
	}
}

func TestUninstallErrors(t *testing.T) {
	failed := errors.New("failed")
	refused := errors.New("operation not permitted")
	for _, test := range []struct {
		name  string
		setUp func(f *fakeFiles, r *fakeRunner, tables *fakeTables, n *fakeNetwork)
		want  string
	}{
		{"record unread", func(f *fakeFiles, _ *fakeRunner, _ *fakeTables, _ *fakeNetwork) {
			f.failRead[Record] = fs.ErrPermission
		}, "cannot read " + Record + ": permission denied"},
		{"record wrong", func(f *fakeFiles, _ *fakeRunner, _ *fakeTables, _ *fakeNetwork) {
			f.files[Record] = file{[]byte("etc/x\n"), 0o644}
		}, Record + `: line 1: "etc/x" is not a clean, absolute path`},
		{"disable", func(_ *fakeFiles, r *fakeRunner, _ *fakeTables, _ *fakeNetwork) {
			r.fail[disable] = failed
		}, disable + ": failed"},
		{"disable the routing", func(_ *fakeFiles, r *fakeRunner, _ *fakeTables, _ *fakeNetwork) {
			r.fail[disableRouting] = failed
		}, disableRouting + ": failed"},
		{"table unread", func(_ *fakeFiles, _ *fakeRunner, tables *fakeTables, _ *fakeNetwork) {
			tables.hasErr = refused
		}, "cannot tell whether there is a table ip ikeward: operation not permitted"},
		{"table", func(_ *fakeFiles, _ *fakeRunner, tables *fakeTables, _ *fakeNetwork) {
			tables.removeErr = refused
		}, "cannot remove table ip ikeward: operation not permitted"},
		{"rules unread", func(_ *fakeFiles, _ *fakeRunner, _ *fakeTables, n *fakeNetwork) {
			n.rulesErr = refused
		}, "cannot read the rules: operation not permitted"},
		{"rule", func(_ *fakeFiles, _ *fakeRunner, _ *fakeTables, n *fakeNetwork) {
			n.ruleErr = refused
		}, "cannot remove rule " + ownRules[0] + ": operation not permitted"},
		{"routes", func(_ *fakeFiles, _ *fakeRunner, _ *fakeTables, n *fakeNetwork) {
			n.routesErr = refused
		}, "cannot remove the routes of table 220: operation not permitted"},
		{"remove", func(f *fakeFiles, _ *fakeRunner, _ *fakeTables, _ *fakeNetwork) {
			f.failRemove["/etc/strongswan.d/zz-ikeward.conf"] = fs.ErrPermission
		}, "cannot remove /etc/strongswan.d/zz-ikeward.conf: permission denied"},
		{"reload", func(_ *fakeFiles, r *fakeRunner, _ *fakeTables, _ *fakeNetwork) {
			r.fail["networkctl reload"] = failed
		}, "networkctl reload: failed"},
		{"link unread", func(_ *fakeFiles, _ *fakeRunner, _ *fakeTables, n *fakeNetwork) {
			n.linkErr = refused
		}, "cannot tell whether there is a link ikeward0: operation not permitted"},
		{"link", func(_ *fakeFiles, _ *fakeRunner, _ *fakeTables, n *fakeNetwork) {
			n.removeErr = refused
		}, "cannot remove link ikeward0: operation not permitted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _ := installed(t)
			r := newFakeRunner()
			tables, network := withNAT(), home()
			test.setUp(f, r, tables, network)
			err := System{Files: f, Runner: r, Tables: tables, Network: network,
				Out: &bytes.Buffer{}}.Uninstall()
			if err == nil || err.Error() != test.want {
				t.Errorf("Uninstall: %v, want %s", err, test.want)
			}
		})
	}
}

// A failed write of the output fails uninstall, once uninstall is done.
func TestUninstallOutputFails(t *testing.T) {
	f, _ := installed(t)
	network := home()
	err := System{Files: f, Runner: newFakeRunner(), Tables: withNAT(), Network: network,
		Out: failingWriter{}}.Uninstall()
	if err == nil || err.Error() != "the pipe is closed" {
		t.Errorf("Uninstall: %v, want the write's error", err)
	}
	if len(f.files) > 0 || len(network.links) > 0 {
		t.Errorf("left %q and the links %v", f.paths(), network.links)
	}
}
