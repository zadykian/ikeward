package host

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
)

// IPv4 asks for name's IPv4 addresses alone, and gives each as such. A name with none, or that
// the resolver cannot resolve, fails it, and says why.
func TestNames(t *testing.T) {
	mapped, other := netip.MustParseAddr("::ffff:198.51.100.10"), netip.MustParseAddr("198.51.100.11")
	var asked []string
	names := Names{lookup: func(_ context.Context, network, host string) ([]netip.Addr, error) {
		asked = append(asked, network+" "+host)
		switch host {
		case "vpn.example.org":
			return []netip.Addr{mapped, other}, nil
		case "v6.example.org":
			return nil, nil
		}
		return nil, errors.New("lookup " + host + ": no such host")
	}}
	addrs, err := names.IPv4(t.Context(), "vpn.example.org")
	if want := []netip.Addr{mapped.Unmap(), other}; err != nil || !slices.Equal(addrs, want) {
		t.Errorf("vpn.example.org: %v, %v; want %v", addrs, err, want)
	}
	for _, test := range []struct{ name, want string }{
		{"v6.example.org", "v6.example.org has no IPv4 address"},
		{"none.example.org", "lookup none.example.org: no such host"},
	} {
		if addrs, err := names.IPv4(t.Context(), test.name); addrs != nil ||
			errorText(err) != test.want {
			t.Errorf("%s: %v, %v; want %q", test.name, addrs, err, test.want)
		}
	}
	want := []string{"ip4 vpn.example.org", "ip4 v6.example.org", "ip4 none.example.org"}
	if !slices.Equal(asked, want) {
		t.Errorf("asked %q, want %q", asked, want)
	}

	// The system's resolver takes an address for itself, and refuses an empty name, asking no one.
	addrs, err = Names{}.IPv4(t.Context(), "192.0.2.1")
	if err != nil || !slices.Equal(addrs, []netip.Addr{netip.MustParseAddr("192.0.2.1")}) {
		t.Errorf("192.0.2.1: %v, %v", addrs, err)
	}
	if addrs, err := (Names{}).IPv4(t.Context(), ""); err == nil {
		t.Errorf("an empty name: %v", addrs)
	}
}
