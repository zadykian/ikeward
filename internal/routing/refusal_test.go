package routing

import (
	"net/netip"
	"testing"
)

// Refusal allows a range of /8 or longer that holds no server's address. It refuses one wider than
// /8, as every address or half of it, and one that holds a server's address, at its edges too. A
// range beside the server's, or an IPv6 server's address, changes nothing.
func TestRefusal(t *testing.T) {
	const wide = "wider than /8, it would take the server's own traffic"
	holds := "it holds 198.51.100.10, the VPN server's address"
	servers := []netip.Addr{netip.MustParseAddr("fd00::1"), server}
	for dst, want := range map[string]string{
		"10.0.0.0/8": "", "11.0.0.0/8": "", "10.0.0.0/7": wide, "0.0.0.0/0": wide,
		"0.0.0.0/1": wide, "128.0.0.0/1": wide, "10.88.0.0/16": "", "198.51.100.0/24": holds,
		"198.51.100.10/32": holds, "198.51.100.8/29": holds, "198.51.101.0/24": "",
		"198.51.100.0/29": "", "198.51.100.11/32": "", "198.0.0.0/8": holds,
	} {
		if got := Refusal(netip.MustParsePrefix(dst), servers); got != want {
			t.Errorf("%s: %q, want %q", dst, got, want)
		}
	}
	mapped := []netip.Addr{netip.MustParseAddr("::ffff:198.51.100.10")}
	if got := Refusal(netip.MustParsePrefix("198.51.100.0/24"), mapped); got != holds {
		t.Errorf("a mapped server's address: %q, want %q", got, holds)
	}
}
