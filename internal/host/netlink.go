package host

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// Netlink is the kernel's addresses, links, and IPv4 rules and routes, which it reads through
// netlink. It changes the routes through files.Link in files.Table, for updown, the rules, for
// routing, and the floors, for both. It removes a link, the rules and files.Table's routes, for
// install and uninstall.
type Netlink struct{}

// errNoLink is the error of a link that does not exist.
var errNoLink = errors.New("there is no such link")

// Addrs returns the addresses on the link named name, IPv4 and IPv6.
func (Netlink) Addrs(name string) ([]netip.Prefix, error) {
	link, err := linkByName(name)
	if err != nil {
		return nil, err
	}
	addrs, err := dump(func() ([]netlink.Addr, error) {
		return netlink.AddrList(link, netlink.FAMILY_ALL)
	})
	return prefixes(addrs), err
}

// Rules returns the IPv4 routing rules.
func (Netlink) Rules() ([]routing.Rule, error) {
	rules, err := dump(func() ([]netlink.Rule, error) { return netlink.RuleList(netlink.FAMILY_V4) })
	return toRules(rules), err
}

// AddRule adds rule, where no rule like it exists: the kernel refuses one with the same selector,
// priority, table and suppress_prefixlength.
func (Netlink) AddRule(rule routing.Rule) error {
	err := netlink.RuleAdd(toRule(rule))
	if errors.Is(err, unix.EEXIST) {
		return nil
	}
	return err
}

// RemoveRule removes the rule at rule's priority that looks up its table, with its
// suppress_prefixlength, where it exists.
func (Netlink) RemoveRule(rule routing.Rule) error {
	err := netlink.RuleDel(toRule(rule))
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

// toRule is rule as netlink adds or removes it, of the IPv4 family, matching every packet.
func toRule(rule routing.Rule) *netlink.Rule {
	r := netlink.NewRule()
	r.Family = netlink.FAMILY_V4
	r.Priority, r.Table, r.SuppressPrefixlen = rule.Priority, rule.Table, rule.SuppressPrefixlen
	return r
}

// SetFloor makes dst unreachable in files.Table at routing.FloorMetric, in place of any route
// there to dst of that metric. The route has no link, so the kernel keeps it as links come and go.
func (Netlink) SetFloor(dst netip.Prefix) error {
	return netlink.RouteReplace(floor(dst))
}

// floor is the unreachable route to dst in files.Table at routing.FloorMetric, as ip route adds
// one: of no link, and of scope universe.
func floor(dst netip.Prefix) *netlink.Route {
	return &netlink.Route{Dst: ipNet(dst), Table: files.Table, Type: unix.RTN_UNREACHABLE,
		Priority: routing.FloorMetric}
}

// RemoveRoutes removes every IPv4 route in table, and returns how many it removed. One that goes
// meanwhile, as with its link, does not count.
func (Netlink) RemoveRoutes(table int) (int, error) {
	routes, err := dump(func() ([]netlink.Route, error) {
		return netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: table},
			netlink.RT_FILTER_TABLE)
	})
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, r := range routes {
		err := netlink.RouteDel(&r)
		switch {
		case errors.Is(err, unix.ESRCH):
		case err != nil:
			return removed, err
		default:
			removed++
		}
	}
	return removed, nil
}

// Routes returns the IPv4 routes in table, each with the name of the link it goes out of.
func (Netlink) Routes(table int) ([]status.Route, error) {
	routes, err := dump(func() ([]netlink.Route, error) {
		return netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: table},
			netlink.RT_FILTER_TABLE)
	})
	if err != nil {
		return nil, err
	}
	links, err := dump(netlink.LinkList)
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	for _, l := range links {
		names[l.Attrs().Index] = l.Attrs().Name
	}
	return toRoutes(routes, names), nil
}

// HasLink is whether the kernel has a link named name.
func (Netlink) HasLink(name string) (bool, error) {
	_, err := linkByName(name)
	if errors.Is(err, errNoLink) {
		return false, nil
	}
	return err == nil, err
}

// RemoveLink removes the link named name, where it exists, with its addresses and routes.
func (Netlink) RemoveLink(name string) error {
	link, err := linkByName(name)
	if errors.Is(err, errNoLink) {
		return nil
	}
	if err != nil {
		return err
	}
	return netlink.LinkDel(link)
}

// SetRoute routes dst through files.Link in files.Table, in place of any route there to dst of the
// same metric, 0.
func (Netlink) SetRoute(dst netip.Prefix) error {
	link, err := linkByName(files.Link)
	if err != nil {
		return fmt.Errorf("%s: %w", files.Link, err)
	}
	return netlink.RouteReplace(throughLink(link.Attrs().Index, dst))
}

// RemoveRoute removes the route to dst through files.Link from files.Table, where it exists. A
// route of another type, as an unreachable one, or through another link, stays.
func (Netlink) RemoveRoute(dst netip.Prefix) error {
	link, err := linkByName(files.Link)
	if errors.Is(err, errNoLink) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", files.Link, err)
	}
	err = netlink.RouteDel(throughLink(link.Attrs().Index, dst))
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

// throughLink is the unicast route to dst through the link of index, in files.Table, as ip route
// adds one with a device and no gateway: of scope link.
func throughLink(index int, dst netip.Prefix) *netlink.Route {
	return &netlink.Route{LinkIndex: index, Table: files.Table, Type: unix.RTN_UNICAST,
		Scope: netlink.SCOPE_LINK, Dst: ipNet(dst)}
}

// ipNet is prefix's network as netlink takes it.
func ipNet(prefix netip.Prefix) *net.IPNet {
	prefix = prefix.Masked()
	return &net.IPNet{IP: prefix.Addr().AsSlice(),
		Mask: net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen())}
}

// linkByName is the link named name, or errNoLink.
func linkByName(name string) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	//nolint:errcheck // the value dropped is the error matched, which errNoLink replaces
	if _, ok := errors.AsType[netlink.LinkNotFoundError](err); ok {
		return nil, errNoLink
	}
	return link, err
}

// dumpTries is how many times dump runs a dump that the kernel interrupts.
const dumpTries = 3

// dump runs list, a netlink dump, again where the kernel's tables changed as it ran, which leaves
// its result incomplete, as when strongSwan replaces its route.
func dump[T any](list func() ([]T, error)) ([]T, error) {
	var items []T
	var err error
	for range dumpTries {
		items, err = list()
		if !errors.Is(err, netlink.ErrDumpInterrupted) {
			return items, err
		}
	}
	return nil, err
}

// prefixes are addrs as prefixes; netlink gives no address but a valid one.
func prefixes(addrs []netlink.Addr) []netip.Prefix {
	var out []netip.Prefix
	for _, a := range addrs {
		if p, ok := prefix(a.IPNet); ok {
			out = append(out, p)
		}
	}
	return out
}

// toRules are rules as routing and status read them. A rule with no suppress_prefixlength has -1,
// as netlink gives it.
func toRules(rules []netlink.Rule) []routing.Rule {
	var out []routing.Rule
	for _, r := range rules {
		out = append(out, routing.Rule{Priority: r.Priority, Table: r.Table,
			SuppressPrefixlen: r.SuppressPrefixlen})
	}
	return out
}

// routeTypes name the kernel's types of route, as ip route does.
var routeTypes = map[int]string{unix.RTN_UNICAST: status.Unicast,
	unix.RTN_UNREACHABLE: status.Unreachable, unix.RTN_BLACKHOLE: "blackhole",
	unix.RTN_PROHIBIT: "prohibit", unix.RTN_THROW: "throw"}

// toRoutes are routes as status reads them, each with the name names gives its link's index. A
// route with no destination is the default route. A type that routeTypes lacks goes by number.
func toRoutes(routes []netlink.Route, names map[int]string) []status.Route {
	var out []status.Route
	for _, r := range routes {
		dst := netip.PrefixFrom(netip.IPv4Unspecified(), 0)
		if p, ok := prefix(r.Dst); ok {
			dst = p
		}
		kind, ok := routeTypes[r.Type]
		if !ok {
			kind = fmt.Sprintf("type %d", r.Type)
		}
		out = append(out, status.Route{Dst: dst, Link: names[r.LinkIndex], Type: kind,
			Metric: r.Priority})
	}
	return out
}

// prefix is n as a prefix, where n is one.
func prefix(n *net.IPNet) (netip.Prefix, bool) {
	if n == nil {
		return netip.Prefix{}, false
	}
	addr, ok := netip.AddrFromSlice(n.IP)
	ones, bits := n.Mask.Size()
	if !ok || bits == 0 {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr.Unmap(), ones), true
}
