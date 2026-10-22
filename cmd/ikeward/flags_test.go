package main

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/host"
	"github.com/zadykian/ikeward/internal/profile/profiletest"
)

// install's flags give the remote network's values. A domain in upper case or with a dot at its
// end is written without them, and the probe is the first domain where not given.
func TestInstallFlags(t *testing.T) {
	f := newFakeSystem(t, "")
	args := []string{"install", "--profile", writeProfile(t, ""), "--dns", "10.1.2.3",
		"--domain", "Example.Test.", "--domain", "example.org", "--floor", "10.0.0.0/8",
		"--floor", "172.16.0.0/12"}
	if err := run(args, f.system); err != nil {
		t.Fatal(err)
	}
	data, _, err := host.Root(f.root).Read(files.SettingsFile)
	want := "\ndns 10.1.2.3\ndomain example.test\ndomain example.org\nprobe example.test\n" +
		"floor 10.0.0.0/8\nfloor 172.16.0.0/12\n"
	if err != nil || !strings.HasSuffix(string(data), want) {
		t.Errorf("%s holds %q, %v; want it to end with %q", files.SettingsFile, data, err, want)
	}
}

// install refuses wrong values of the remote network's, as a mistake on the command line, before it
// reads the profile or asks for root. A wrong first domain is no wrong probe too.
func TestInstallFlagMistakes(t *testing.T) {
	wide := "wider than /8, it would take the server's own traffic"
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--domain", "x.example"}, "install needs --dns ADDR and --domain NAME"},
		{[]string{"--dns", "10.1.2.3"}, "install needs --dns ADDR and --domain NAME"},
		{[]string{"--dns", "dns.example", "--domain", "x.example"},
			`--dns "dns.example" is no address`},
		{[]string{"--dns", "2001:db8::1", "--domain", "x.example"},
			"the resolver, 2001:db8::1, is no IPv4 unicast address"},
		{[]string{"--dns", "10.1.2.3", "--domain", "x.example", "--domain", "a b"},
			`the domain "a b" is no domain name`},
		{[]string{"--dns", "10.1.2.3", "--domain", "~example.test"},
			`the domain "~example.test" is no domain name`},
		{[]string{"--dns", "10.1.2.3", "--domain", "x.example", "--probe", "x..example"},
			`the probe, "x..example", is no domain name`},
		{[]string{"--dns", "10.1.2.3", "--domain", "x.example", "--floor", "10.0.0.0"},
			`--floor "10.0.0.0" is no range`},
		{[]string{"--dns", "10.1.2.3", "--domain", "x.example", "--floor", "0.0.0.0/1"},
			"--floor 0.0.0.0/1: " + wide},
		{[]string{"--dns", "10.1.2.3", "--domain", "x.example", "--floor", "10.1.0.0/16",
			"--floor", "10.1.0.0/16"}, "the floor 10.1.0.0/16 comes twice"},
		{[]string{"--dns", "nowhere", "--domain", "x.example", "--floor", "0.0.0.0/0"},
			"--dns \"nowhere\" is no address\n--floor 0.0.0.0/0: " + wide},
	} {
		args := append([]string{"install", "--profile", "missing.zip"}, test.args...)
		err := run(args, system{euid: func() int { return 1000 }})
		want := test.want + " (see ikeward install --help)"
		if err == nil || err.Error() != want || exitStatus(err) != 2 {
			t.Errorf("%q: %v, want %s, and exit status 2", test.args, err, want)
		}
	}
}

// install, and its dry run, refuse a --floor that holds the VPN server's address, as its name
// resolves now, as a mistake on the command line. They name each such floor and the address, and
// change nothing.
func TestInstallServersFloor(t *testing.T) {
	holds := ": it holds 198.51.100.10, the VPN server's address"
	want := "--floor 198.51.0.0/16" + holds + "\n--floor 198.51.100.0/24" + holds +
		" (see ikeward install --help)"
	for _, more := range [][]string{nil, {"--dry-run"}} {
		f := newFakeSystem(t, "secret\n")
		err := run(installArgs(writeProfile(t, "secret"), append([]string{"--floor",
			"198.51.0.0/16", "--floor", "10.0.0.0/8", "--floor", "198.51.100.0/24"}, more...)...),
			f.system)
		if err == nil || err.Error() != want || exitStatus(err) != 2 {
			t.Errorf("%q: %v, want %s, and exit status 2", more, err, want)
		}
		if got := f.installed(t); len(got) > 0 || len(f.ran.commands) > 0 ||
			!slices.Equal(f.names.asked, []string{profiletest.Server}) {
			t.Errorf("%q: wrote %q, ran %q, and resolved %q", more, got, f.ran.commands,
				f.names.asked)
		}
	}
}

// Where the VPN server's name does not resolve, install fails with a floor given, and says why.
// With no floor, it resolves nothing, and installs.
func TestInstallServerUnresolved(t *testing.T) {
	f := newFakeSystem(t, "secret\n")
	f.names.down = true
	err := run(installArgs(writeProfile(t, "secret")), f.system)
	want := "cannot resolve vpn.example.org, the VPN server, whose address no --floor may hold: " +
		"lookup vpn.example.org: no such host"
	if err == nil || err.Error() != want || exitStatus(err) != 1 {
		t.Errorf("%v, want %s, and exit status 1", err, want)
	}
	if got := f.installed(t); len(got) > 0 || len(f.ran.commands) > 0 {
		t.Errorf("wrote %q, and ran %q", got, f.ran.commands)
	}

	f = newFakeSystem(t, "secret\n")
	f.names.down = true
	err = run([]string{"install", "--profile", writeProfile(t, "secret"), "--dns", "10.1.2.3",
		"--domain", "example.test"}, f.system)
	if err != nil || len(f.names.asked) > 0 || !slices.Contains(f.installed(t), files.SettingsFile) {
		t.Errorf("with no floor: %v, and resolved %q", err, f.names.asked)
	}
}

// A profile that names the VPN server by its address gives that address, and none is resolved.
func TestServerAddrs(t *testing.T) {
	f := newFakeSystem(t, "")
	for server, want := range map[string]string{"198.51.100.20": "198.51.100.20",
		"::ffff:198.51.100.20": "198.51.100.20", "2001:db8::1": "2001:db8::1"} {
		addrs, err := f.serverAddrs(t.Context(), server)
		if err != nil || !slices.Equal(addrs, []netip.Addr{netip.MustParseAddr(want)}) {
			t.Errorf("%s: %v, %v; want %s", server, addrs, err, want)
		}
	}
	command := newInstall(f.system)
	command.SetContext(t.Context())
	err := f.clearOfServer(command, "198.51.100.20", []netip.Prefix{
		netip.MustParsePrefix("198.51.100.0/24")})
	want := "--floor 198.51.100.0/24: it holds 198.51.100.20, the VPN server's address " +
		"(see install --help)"
	if err == nil || err.Error() != want || exitStatus(err) != 2 || len(f.names.asked) > 0 {
		t.Errorf("%v, want %s, and exit status 2; resolved %q", err, want, f.names.asked)
	}
}
