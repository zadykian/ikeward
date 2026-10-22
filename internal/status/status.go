// Package status checks the VPN as the watchdog does, and changes nothing:
//
//   - through VICI, that strongSwan's IKE SA and CHILD SA are up;
//   - through netlink, that the address the server assigned is on ikeward0, the tunnel's link. Then
//     that ikeward's rules look up main, then table 220, which routes each range the CHILD SA
//     grants through ikeward0;
//   - with a DNS query out ikeward0 alone, that the resolver of files.SettingsFile answers with an
//     address. The result's Err wraps ErrAnswer where the resolver answers without one;
//   - through resolvectl, that resolved sends the remote network's names on ikeward0 to that
//     resolver, and through netlink, that ikeward0 holds its own address, which keeps them there;
//   - through netlink again, that table 220 holds the floors, for while the tunnel is down: one for
//     each range of the settings, and one for each range routing.Learned records;
//   - through nftables, that ikeward's NAT table masquerades what leaves by ikeward0.
//
// Each check reaches the system through an interface, which unit tests replace with a fake.
package status

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// IKESA is an IKE SA of the connection, as strongSwan lists it.
type IKESA struct {
	// State is ESTABLISHED once the SA is up.
	State string
	// Remote is the VPN server's address.
	Remote string
	// VIPs are the addresses the server assigned.
	VIPs []string
	// Children are its CHILD SAs.
	Children []ChildSA
}

// ChildSA is a CHILD SA of an IKE SA.
type ChildSA struct {
	// Name is the child's name in the connection.
	Name string
	// State is INSTALLED once the SA is up.
	State string
	// LocalTS and RemoteTS are its traffic selectors.
	LocalTS, RemoteTS []string
}

// Tunnel is strongSwan, which VICI reaches.
type Tunnel interface {
	// IKESAs returns the IKE SAs of the connection named conn.
	IKESAs(ctx context.Context, conn string) ([]IKESA, error)
}

// The types of route that the checks tell apart, as ip route names them. A unicast route takes
// traffic on to a gateway or a link. An unreachable one takes none on, and its sender learns so.
const (
	Unicast     = "unicast"
	Unreachable = "unreachable"
)

// Route is an IPv4 route.
type Route struct {
	Dst netip.Prefix
	// Link names the link the route goes out of, where it names one.
	Link string
	// Type is the route's type, as Unicast or Unreachable, and Metric its metric.
	Type   string
	Metric int
}

// Network is the kernel's addresses, and its IPv4 rules and routes, which netlink reads.
type Network interface {
	// Addrs returns the addresses on the link named link, IPv4 and IPv6.
	Addrs(link string) ([]netip.Prefix, error)
	// Rules returns the IPv4 routing rules.
	Rules() ([]routing.Rule, error)
	// Routes returns the IPv4 routes in table.
	Routes(table int) ([]Route, error)
}

// Runner runs commands.
type Runner interface {
	// Run runs the program name with args and no input, and returns what it printed on stdout.
	Run(name string, args ...string) (string, error)
}

// Resolver is a DNS server, asked directly.
type Resolver interface {
	// Lookup asks the server at server, an address and port, for name's IPv4 addresses, out
	// files.Link alone, and fails unless it answers with at least one. It asks again while no
	// reply comes, until ctx ends, so that one lost query or reply fails nothing. Its error wraps
	// ErrAnswer where a reply comes, but with an error code or no address.
	Lookup(ctx context.Context, server, name string) error
}

// ErrAnswer starts Lookup's error where the server answers, but with no address, as in "it
// answers SERVFAIL". The resolver check's answer then crossed the tunnel both ways, which a
// restart of strongSwan does not change, as the watchdog's unchanged says.
var ErrAnswer = errors.New("it answers")

// NAT is routing.NATTable in nftables, which netlink reads.
type NAT interface {
	// Connections returns how many connections the table's rule has NATed since the table was last
	// made, by ikeward routing or the watchdog. It fails with a routing.Missing where the table is
	// missing, or holds more or other than routing.NATChain and routing.NATRule.
	Connections() (uint64, error)
}

// System is what the checks read.
type System struct {
	Tunnel   Tunnel
	Network  Network
	Resolver Resolver
	// Runner runs resolvectl, and Files hold files.SettingsFile and routing.Learned.
	Runner Runner
	Files  routing.Reader
	NAT    NAT
}

// Result is what a check found.
type Result struct {
	// Name names the check.
	Name string
	// OK is whether it passed.
	OK bool
	// Detail says what it found.
	Detail string
	// Err is the error behind a failure, where the check keeps it: the resolver check keeps
	// Lookup's, so that the watchdog tells an answer from none. The checks that read
	// files.SettingsFile keep routing.ReadSettings' error.
	Err error
}

// String is r as status prints it: a mark, the check's name, and what it found, in columns.
func (r Result) String() string {
	mark := "FAIL"
	if r.OK {
		mark = "ok"
	}
	return fmt.Sprintf("%-4s  %-8s  %s", mark, r.Name, r.Detail)
}

// Failed counts the results that did not pass.
func Failed(results []Result) int {
	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
		}
	}
	return failed
}

// askTimeout bounds the wait for strongSwan's answer, and resolverTimeout that for the resolver's.
const (
	askTimeout      = 10 * time.Second
	resolverTimeout = 5 * time.Second
)

// ResolverCheck names the check of the remote resolver, through the tunnel.
const ResolverCheck = "resolver"

// Check runs the checks, each once, and returns what each found. In order, they check the IKE SA,
// the CHILD SA, the address on ikeward0, the rules, and table 220's routes. Then come the resolver,
// resolved's settings of ikeward0 with its own address, table 220's floors, and the NAT table.
func (s System) Check(ctx context.Context) []Result {
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	t := newTunnel(s.Tunnel.IKESAs(ctx, files.Connection))
	// The address is checked against the one the server assigned, and the routes against the
	// ranges it granted. The resolver is asked where none of the five before it failed, whatever
	// the last three find.
	results := []Result{t.ikeSA(), t.childSA(), s.address(t), s.rule(), s.route(t)}
	settings, err := routing.ReadSettings(s.Files)
	if err != nil {
		for _, name := range []string{ResolverCheck, DNSCheck, FloorCheck} {
			results = append(results, Result{Name: name, Detail: err.Error(), Err: err})
		}
		return append(results, s.nat())
	}
	return append(results, s.resolver(ctx, settings, Failed(results)), s.dns(settings),
		s.floor(settings), s.nat())
}

// address is the address check: the address the server assigned is on files.Link, where
// strongSwan puts it. The lookups resolved sends on files.Link leave from it, and so match the
// tunnel. It names the link's IPv4 addresses alone, as files.LinkULA is the DNS check's.
func (s System) address(t tunnel) Result {
	r := Result{Name: "address"}
	if !t.vip.IsValid() {
		r.Detail = "no address from the server to look for on " + files.Link + ": " + t.noAddress()
		return r
	}
	prefixes, err := s.Network.Addrs(files.Link)
	if err != nil {
		r.Detail = fmt.Sprintf("cannot read the addresses of %s: %v", files.Link, err)
		return r
	}
	var v4 []netip.Prefix
	for _, p := range prefixes {
		if p.Addr() == t.vip {
			r.OK = true
			r.Detail = fmt.Sprintf("%s is on %s", t.vip, files.Link)
			return r
		}
		if p.Addr().Is4() {
			v4 = append(v4, p)
		}
	}
	has := "no IPv4 address"
	if len(v4) > 0 {
		has = join(v4)
	}
	r.Detail = fmt.Sprintf("%s is not on %s, which has %s", t.vip, files.Link, has)
	return r
}

// resolver is the resolver check: the settings' resolver answers a query for their probe, which
// goes out files.Link alone, through the tunnel. It catches a tunnel whose SAs, address and routes
// look right but that carries nothing. Where failed of the checks before it failed, it asks
// nothing, as the query would not go through the tunnel.
func (s System) resolver(ctx context.Context, settings files.Settings, failed int) Result {
	r := Result{Name: ResolverCheck}
	if failed > 0 {
		r.Detail = "not asked, as the tunnel's checks failed"
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, resolverTimeout)
	defer cancel()
	server := net.JoinHostPort(settings.DNS.String(), "53")
	if err := s.Resolver.Lookup(ctx, server, settings.Probe); err != nil {
		r.Err = err
		r.Detail = fmt.Sprintf("%s gives no address for %s: %v", settings.DNS, settings.Probe, err)
		// An answer, if with no address, crossed the tunnel both ways.
		if errors.Is(err, ErrAnswer) {
			r.Detail += " through the tunnel"
		}
		return r
	}
	r.OK = true
	r.Detail = fmt.Sprintf("%s answers for %s through the tunnel", settings.DNS, settings.Probe)
	return r
}

// join is prefixes as a list.
func join(prefixes []netip.Prefix) string {
	items := make([]string, len(prefixes))
	for i, p := range prefixes {
		items[i] = p.String()
	}
	return strings.Join(items, ", ")
}
