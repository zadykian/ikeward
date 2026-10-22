package status

import (
	"net/netip"
	"slices"
	"testing"
)

// Grants finds whether an IKE SA that is up with its CHILD SA of the connection grants a range,
// as updown asks while an SA of the connection goes down.
func TestGrants(t *testing.T) {
	sa := func(state, child string, remote ...string) IKESA {
		return IKESA{State: state, VIPs: []string{"203.0.113.130"},
			Children: []ChildSA{{Name: "ikeward", State: child, RemoteTS: remote}}}
	}
	granted := []string{"192.0.2.0/24", "198.18.0.0/24"}
	for _, test := range []struct {
		name string
		sas  []IKESA
		want bool
	}{
		{"up", []IKESA{sa("ESTABLISHED", "INSTALLED", granted...)}, true},
		{"rekeying", []IKESA{sa("REKEYING", "REKEYING", granted...)}, true},
		{"the one with its child", []IKESA{sa("ESTABLISHED", "CREATED", granted...),
			sa("ESTABLISHED", "INSTALLED", "fd00::/8", "198.18.0.0/24[tcp/80]")}, true},
		{"another range", []IKESA{sa("ESTABLISHED", "INSTALLED", "192.0.2.0/24")}, false},
		{"none", nil, false},
		{"connecting", []IKESA{sa("CONNECTING", "CREATED", granted...)}, false},
		{"deleting", []IKESA{sa("DELETING", "INSTALLED", granted...)}, false},
		{"child deleted", []IKESA{sa("ESTABLISHED", "DELETED", granted...)}, false},
		{"no child", []IKESA{{State: "ESTABLISHED", VIPs: []string{"203.0.113.130"}}}, false},
		{"other child", []IKESA{{State: "ESTABLISHED", Children: []ChildSA{{Name: "other",
			State: "INSTALLED", RemoteTS: granted}}}}, false},
	} {
		if got := Grants(test.sas, netip.MustParsePrefix("198.18.0.0/24")); got != test.want {
			t.Errorf("%s: %v, want %v", test.name, got, test.want)
		}
	}
}

// Ranges reads the IPv4 subnets of strongSwan's traffic selectors, once each and in order. A
// range of addresses takes the least subnet that holds it, as updown's PLUTO_PEER_CLIENT does.
func TestRanges(t *testing.T) {
	got := Ranges([]string{"192.0.2.0/24", "198.18.0.0/24[tcp/http]", "192.0.2.0/24[udp]",
		"10.0.0.1..10.0.0.5", "10.0.1.0..10.0.1.255", "0.0.0.0..255.255.255.255", "0.0.0.0/0",
		"203.0.113.7/24", "fd00::/8", "fd00::1..fd00::5", "10.0.0.1..fd00::1", "dynamic", ""})
	var want []netip.Prefix
	for _, p := range []string{"192.0.2.0/24", "198.18.0.0/24", "10.0.0.0/29", "10.0.1.0/24",
		"0.0.0.0/0", "203.0.113.0/24"} {
		want = append(want, netip.MustParsePrefix(p))
	}
	if !slices.Equal(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
}
