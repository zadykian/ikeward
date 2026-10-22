package host

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// netOf is cidr as netlink gives it: its address, not its network's, with the mask.
func netOf(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	n.IP = ip
	return n
}

// The addresses, rules and routes that netlink gave on a live server, with the VPN up, read as
// status reads them. ikeward0's own IPv6 address, ikeward's rule and its floor are there too. A
// route with no destination is the default route, and each names its link and metric. Made-up
// routes of other types go by ip route's names for them, but for one that goes by its number.
func TestConversions(t *testing.T) {
	addrs := []netlink.Addr{{IPNet: netOf(t, "203.0.113.130/32")}, {IPNet: netOf(t, "10.1.2.3/8")},
		{IPNet: &net.IPNet{IP: net.ParseIP("203.0.113.131"), Mask: net.CIDRMask(32, 32)}},
		{}, {IPNet: &net.IPNet{IP: net.IP{1, 2}, Mask: net.CIDRMask(8, 32)}},
		{IPNet: &net.IPNet{IP: net.IPv4(10, 0, 0, 1)}}, {IPNet: netOf(t, "fd22:851f:35dd::1/128")}}
	want := []netip.Prefix{netip.MustParsePrefix("203.0.113.130/32"),
		netip.MustParsePrefix("10.1.2.3/8"), netip.MustParsePrefix("203.0.113.131/32"),
		netip.MustParsePrefix("fd22:851f:35dd::1/128")}
	if got := prefixes(addrs); !slices.Equal(got, want) {
		t.Errorf("prefixes: %v, want %v", got, want)
	}

	rules := []netlink.Rule{{Priority: 0, Table: 255, SuppressPrefixlen: -1},
		{Priority: 32000, Table: 254, SuppressPrefixlen: 0},
		{Priority: 220, Table: 220, SuppressPrefixlen: -1}}
	wantRules := []routing.Rule{{Priority: 0, Table: 255, SuppressPrefixlen: -1},
		{Priority: 32000, Table: 254, SuppressPrefixlen: 0},
		{Priority: 220, Table: 220, SuppressPrefixlen: -1}}
	if got := toRules(rules); !slices.Equal(got, wantRules) {
		t.Errorf("rules: %v, want %v", got, wantRules)
	}

	routes := []netlink.Route{
		{Dst: netOf(t, "10.88.0.0/16"), LinkIndex: 4, Table: 220, Type: unix.RTN_UNICAST},
		{Gw: net.ParseIP("198.51.100.1"), LinkIndex: 2, Type: unix.RTN_UNICAST, Priority: 100},
		{Dst: netOf(t, "10.0.0.0/8"), Src: net.IP{203, 0, 113, 7}, LinkIndex: 9,
			Type: unix.RTN_UNICAST},
		{Dst: netOf(t, "10.88.0.0/16"), Type: unix.RTN_UNREACHABLE, Priority: 4000},
		{Dst: netOf(t, "10.88.88.0/24"), Type: unix.RTN_BLACKHOLE},
		{Dst: netOf(t, "10.88.1.0/24"), Type: unix.RTN_PROHIBIT},
		{Dst: netOf(t, "10.88.200.0/24"), Type: unix.RTN_THROW},
		{Dst: netOf(t, "10.88.201.0/24"), Type: unix.RTN_NAT},
	}
	wantRoutes := []status.Route{
		{Dst: netip.MustParsePrefix("10.88.0.0/16"), Link: "ikeward0", Type: "unicast"},
		{Dst: netip.MustParsePrefix("0.0.0.0/0"), Link: "eth0", Type: "unicast", Metric: 100},
		{Dst: netip.MustParsePrefix("10.0.0.0/8"), Type: "unicast"},
		{Dst: netip.MustParsePrefix("10.88.0.0/16"), Type: "unreachable", Metric: 4000},
		{Dst: netip.MustParsePrefix("10.88.88.0/24"), Type: "blackhole"},
		{Dst: netip.MustParsePrefix("10.88.1.0/24"), Type: "prohibit"},
		{Dst: netip.MustParsePrefix("10.88.200.0/24"), Type: "throw"},
		{Dst: netip.MustParsePrefix("10.88.201.0/24"), Type: "type 10"},
	}
	names := map[int]string{1: "lo", 2: "eth0", 3: "docker0", 4: "ikeward0"}
	if got := toRoutes(routes, names); !slices.Equal(got, wantRoutes) {
		t.Errorf("routes: %v, want %v", got, wantRoutes)
	}
}

// updown's route goes through the tunnel's link in table 220, with no gateway, as ip route adds
// one: unicast, of scope link. It names no source, which the kernel takes from the link: the
// address the server assigned. A prefix with host bits set routes its network.
func TestThroughLink(t *testing.T) {
	for _, dst := range []string{"10.88.0.0/16", "10.88.3.130/16"} {
		got := throughLink(7, netip.MustParsePrefix(dst))
		want := &netlink.Route{LinkIndex: 7, Dst: netOf(t, "10.88.0.0/16"), Table: 220,
			Type: unix.RTN_UNICAST, Scope: netlink.SCOPE_LINK}
		want.Dst.IP = want.Dst.IP.To4()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", dst, got, want)
		}
	}
}

// A rule goes to netlink as one that matches every packet, of the IPv4 family, with no other
// selector set, which netlink.NewRule gives. The floor has no link, and a prefix with host bits
// set makes it unreachable as its network.
func TestToRuleAndFloor(t *testing.T) {
	want := netlink.NewRule()
	want.Family, want.Priority, want.Table, want.SuppressPrefixlen = netlink.FAMILY_V4, 32001, 220, 0
	if got := toRule(routing.Rules[1]); !reflect.DeepEqual(got, want) {
		t.Errorf("toRule: %+v, want %+v", got, want)
	}
	for _, dst := range []string{"10.88.0.0/16", "10.88.3.130/16"} {
		wantFloor := &netlink.Route{Dst: netOf(t, "10.88.0.0/16"), Table: 220,
			Type: unix.RTN_UNREACHABLE, Priority: 4000}
		wantFloor.Dst.IP = wantFloor.Dst.IP.To4()
		if got := floor(netip.MustParsePrefix(dst)); !reflect.DeepEqual(got, wantFloor) {
			t.Errorf("%s: %+v, want %+v", dst, got, wantFloor)
		}
	}
}

// dump runs a dump again where the kernel interrupted it, up to three times, and returns what the
// last run gave, but nothing that an interrupted run gave.
func TestDump(t *testing.T) {
	interrupted := fmt.Errorf("route list: %w", netlink.ErrDumpInterrupted)
	failed := errors.New("operation not permitted")
	for _, test := range []struct {
		name string
		errs []error
		want []int
		err  error
	}{
		{"first", []error{nil}, []int{1}, nil},
		{"second", []error{interrupted, nil}, []int{2}, nil},
		{"third", []error{interrupted, interrupted, nil}, []int{3}, nil},
		{"never", []error{interrupted, interrupted, interrupted, nil}, nil, interrupted},
		{"failed", []error{failed, nil}, []int{1}, failed},
	} {
		runs := 0
		// Each run gives its number.
		got, err := dump(func() ([]int, error) {
			runs++
			return []int{runs}, test.errs[runs-1]
		})
		if !slices.Equal(got, test.want) || !errors.Is(err, test.err) {
			t.Errorf("%s: ran %d times, and returned %v, %v; want %v, %v", test.name, runs, got,
				err, test.want, test.err)
		}
		if test.name == "never" && runs != dumpTries {
			t.Errorf("never: ran %d times, want %d", runs, dumpTries)
		}
	}
}
