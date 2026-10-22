package status

import (
	"errors"
	"net/netip"
	"testing"
)

// The DNS check fails, and says why, where resolvectl fails or says nothing of ikeward0. It fails
// too where resolved's settings of ikeward0 are not networkd's: other servers may be among its own,
// but it has exactly networkd's domains.
func TestDNS(t *testing.T) {
	for _, test := range []struct {
		name string
		out  string
		err  error
		want string
	}{
		{"healthy", capturedResolved, nil,
			"ok    DNS       resolved sends ~example.test lookups on ikeward0 to 192.0.2.10"},
		{"no link", "", errors.New(`exit status 1: Failed to resolve interface "ikeward0": ` +
			"No such device"),
			`FAIL  DNS       resolvectl --json=short status ikeward0: exit status 1: ` +
				`Failed to resolve interface "ikeward0": No such device`},
		{"not JSON", "Link 3 (ikeward0)\n", nil,
			"FAIL  DNS       cannot read what resolvectl printed: invalid character 'L' looking " +
				"for beginning of value"},
		{"other link", `[{"ifname":"eth0","ifindex":2}]`, nil,
			"FAIL  DNS       resolvectl says nothing of ikeward0"},
		{"none", `[]`, nil, "FAIL  DNS       resolvectl says nothing of ikeward0"},
		{"no settings", `[{"ifname":"ikeward0","ifindex":3,"defaultRoute":false,"dnssec":"no",` +
			`"dnsOverTLS":"no","llmnr":"no","mDNS":"no"}]`, nil,
			"FAIL  DNS       ikeward0: 192.0.2.10 is not among its DNS servers; it has no domain " +
				"~example.test; resolved sends it no lookup"},
		{"other settings", `[{"ifname":"ikeward0","defaultRoute":true,"servers":[` +
			`{"addressString":"192.0.2.11"},{"addressString":"192.0.2.10:5353"}],` +
			`"searchDomains":[{"name":"example.test","routeOnly":false}],` +
			`"scopes":[{"protocol":"llmnr"},{"protocol":"dns"}]}]`, nil,
			"FAIL  DNS       ikeward0: 192.0.2.10 is not among its DNS servers; it has no domain " +
				"~example.test; it has the domain example.test too; lookups of any name may go to it"},
		{"more servers", `[{"ifname":"eth0"},{"ifname":"ikeward0","servers":[` +
			`{"addressString":"1.1.1.1"},{"addressString":"192.0.2.10"}],"searchDomains":[` +
			`{"name":"example.test","routeOnly":true}],"scopes":[{"protocol":"dns"}]}]`, nil,
			"ok    DNS       resolved sends ~example.test lookups on ikeward0 to 192.0.2.10"},
		{"more domains", `[{"ifname":"ikeward0","servers":[{"addressString":"192.0.2.10"}],` +
			`"searchDomains":[{"name":"example.test","routeOnly":true},` +
			`{"name":"other.test","routeOnly":true},{"name":"example.org","routeOnly":false}],` +
			`"scopes":[{"protocol":"dns"}]}]`, nil,
			"FAIL  DNS       ikeward0: it has the domain ~other.test too; it has the domain " +
				"example.org too"},
		// Other domains alone, without the remote network's.
		{"other domains", `[{"ifname":"ikeward0","servers":[{"addressString":"192.0.2.10"}],` +
			`"searchDomains":[{"name":"one.test","routeOnly":true},` +
			`{"name":"two.test","routeOnly":true}],"scopes":[{"protocol":"dns"}]}]`, nil,
			"FAIL  DNS       ikeward0: it has no domain ~example.test; it has the domain " +
				"~one.test too; it has the domain ~two.test too"},
	} {
		f := healthy()
		f.runner.out, f.runner.err = test.out, test.err
		if got := f.system().dns(testSettings).String(); got != test.want {
			t.Errorf("%s:\n%s\nwant:\n%s", test.name, got, test.want)
		}
	}
}

// The DNS check wants each of the settings' domains, and their resolver.
func TestDNSSettings(t *testing.T) {
	f := healthy()
	f.runner.out = `[{"ifname":"ikeward0","servers":[{"addressString":"10.1.2.3"}],` +
		`"searchDomains":[{"name":"example.test","routeOnly":true},` +
		`{"name":"other.test","routeOnly":true}],"scopes":[{"protocol":"dns"}]}]`
	two := testSettings
	two.DNS, two.Domains = netip.MustParseAddr("10.1.2.3"), []string{"example.test", "other.test"}
	want := "ok    DNS       resolved sends ~example.test ~other.test lookups on ikeward0 to 10.1.2.3"
	if got := f.system().dns(two).String(); got != want {
		t.Errorf("%s, want %s", got, want)
	}
	two.Domains = append(two.Domains, "example.org")
	want = "FAIL  DNS       ikeward0: it has no domain ~example.org"
	if got := f.system().dns(two).String(); got != want {
		t.Errorf("%s, want %s", got, want)
	}
}

func TestFailed(t *testing.T) {
	results := []Result{{Name: "a", OK: true}, {Name: "b"}, {Name: "c", OK: true}, {Name: "d"}}
	if got := Failed(results); got != 2 {
		t.Errorf("Failed is %d, want 2", got)
	}
	if got := Failed(nil); got != 0 {
		t.Errorf("Failed of none is %d, want 0", got)
	}
	if got, want := (Result{Name: "rule", Detail: "no rule"}).String(),
		"FAIL  rule      no rule"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

// The DNS check finds ikeward0's own address by the address alone, whatever its prefix's length. No
// other IPv6 address stands in for it, a link-local one among them.
func TestDNSOwnAddress(t *testing.T) {
	for _, test := range []struct {
		name  string
		addrs []netip.Prefix
		ok    bool
	}{
		{"own alone", []netip.Prefix{own}, true},
		{"own, as a /64", []netip.Prefix{netip.MustParsePrefix("fd22:851f:35dd::1/64")}, true},
		{"link-local", []netip.Prefix{netip.MustParsePrefix("fe80::1/64"),
			netip.MustParsePrefix("203.0.113.130/32")}, false},
		{"another in its network", []netip.Prefix{netip.MustParsePrefix("fd22:851f:35dd::2/128")},
			false},
		{"none", nil, false},
	} {
		f := healthy()
		f.network.addrs["ikeward0"] = test.addrs
		if got := f.system().dns(testSettings); got.OK != test.ok || !test.ok && got.String() != ownGone {
			t.Errorf("%s: %s", test.name, got)
		}
	}
}
