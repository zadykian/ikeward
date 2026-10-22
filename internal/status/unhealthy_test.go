package status

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/routing"
)

// newChild is a CHILD SA named name, in state.
func newChild(name, state string) ChildSA {
	return ChildSA{Name: name, State: state, LocalTS: []string{"203.0.113.9/32"},
		RemoteTS: []string{"10.88.0.0/16"}}
}

// cases change the healthy fakes, as each check fails.
var cases = []change{
	{"strongSwan stopped", func(f fakes) {
		f.tunnel.err = errors.New("/run/charon.vici: no such file or directory")
	}, map[string]string{
		"IKE SA":   "FAIL  IKE SA    cannot ask strongSwan: /run/charon.vici: no such file or directory",
		"CHILD SA": "FAIL  CHILD SA  not known, as strongSwan could not be asked",
		"address": "FAIL  address   no address from the server to look for on ikeward0: " +
			"strongSwan could not be asked",
		"route": "FAIL  route     no range of the server's to look for in table 220: strongSwan " +
			"could not be asked",
	}},
	// ikeward0 keeps its own address, and with it resolved's DNS scope, so the DNS check passes.
	// The floor stays as the route through ikeward0 goes.
	{"no SA", func(f fakes) {
		f.tunnel.sas = nil
		f.network.addrs["ikeward0"] = []netip.Prefix{own}
		f.network.routes[220] = []Route{floor}
	}, map[string]string{
		"IKE SA":   "FAIL  IKE SA    strongSwan has no IKE SA of ikeward",
		"CHILD SA": "FAIL  CHILD SA  no IKE SA of ikeward is up to carry it",
		"address":  "FAIL  address   no address from the server to look for on ikeward0: no IKE SA is up",
		"route":    noRange + "no IKE SA is up",
	}},
	// ikeward0 lost its own address, as the kernel takes it off where IPv6 is disabled at runtime.
	// strongSwan's address keeps resolved's DNS scope, but the DNS check fails all the same.
	{"own address gone", func(f fakes) {
		f.network.addrs["ikeward0"] = f.network.addrs["ikeward0"][:1]
	}, map[string]string{"DNS": ownGone}},
	// Without strongSwan's address too, resolved sends ikeward0 no lookup.
	{"own address gone, no SA", func(f fakes) {
		f.tunnel.sas = nil
		f.network.addrs["ikeward0"] = nil
		f.network.routes[220] = []Route{floor}
		f.runner.out = `[{"ifname":"ikeward0","ifindex":3,"defaultRoute":false,` +
			`"servers":[{"addressString":"192.0.2.10"}],"searchDomains":[` +
			`{"name":"example.test","routeOnly":true}]}]`
	}, map[string]string{
		"IKE SA":   "FAIL  IKE SA    strongSwan has no IKE SA of ikeward",
		"CHILD SA": "FAIL  CHILD SA  no IKE SA of ikeward is up to carry it",
		"address":  "FAIL  address   no address from the server to look for on ikeward0: no IKE SA is up",
		"route":    noRange + "no IKE SA is up",
		"DNS": strings.Replace(ownGone, "ikeward0: ", "ikeward0: resolved sends it no lookup; ",
			1),
	}},
	{"connecting", func(f fakes) {
		f.tunnel.sas = []IKESA{{State: "CONNECTING"}, {State: "DELETING"}}
	}, map[string]string{
		"IKE SA":   "FAIL  IKE SA    ikeward is CONNECTING, DELETING",
		"CHILD SA": "FAIL  CHILD SA  no IKE SA of ikeward is up to carry it",
		"address":  "FAIL  address   no address from the server to look for on ikeward0: no IKE SA is up",
		"route":    noRange + "no IKE SA is up",
	}},
	{"replaced", func(f fakes) {
		up := f.tunnel.sas[0]
		f.tunnel.sas = []IKESA{
			{State: "ESTABLISHED", VIPs: []string{"203.0.113.7"}},
			{State: "CONNECTING"},
			up,
			{State: "ESTABLISHED", VIPs: []string{"203.0.113.8"},
				Children: []ChildSA{newChild("ikeward", "INSTALLED")}},
		}
	}, nil},
	{"IKE SA rekeying", func(f fakes) {
		f.tunnel.sas[0].State = "REKEYING"
	}, map[string]string{
		"IKE SA": "ok    IKE SA    ikeward is REKEYING, with 203.0.113.130 from the server",
	}},
	{"IKE SA rekeyed", func(f fakes) {
		up := f.tunnel.sas[0]
		up.State = "REKEYING"
		f.tunnel.sas = []IKESA{{State: "ESTABLISHED", VIPs: []string{"203.0.113.7"}}, up}
	}, map[string]string{
		"IKE SA": "ok    IKE SA    ikeward is REKEYING, with 203.0.113.130 from the server",
	}},
	{"child rekeying", func(f fakes) {
		f.tunnel.sas[0].Children = []ChildSA{newChild("other", "INSTALLED"),
			newChild("ikeward", "REKEYED"), newChild("ikeward", "REKEYING")}
	}, map[string]string{
		"CHILD SA": "ok    CHILD SA  ikeward is REKEYING: 203.0.113.9/32 === 10.88.0.0/16",
	}},
	{"child rekeyed", func(f fakes) {
		f.tunnel.sas[0].Children = []ChildSA{newChild("other", "INSTALLED"),
			newChild("ikeward", "REKEYED"), newChild("ikeward", "DELETING"),
			newChild("ikeward", "REKEYED")}
	}, map[string]string{
		"CHILD SA": "FAIL  CHILD SA  ikeward is REKEYED, DELETING",
		"route":    noRange + "no CHILD SA is up",
	}},
	{"no child", func(f fakes) {
		f.tunnel.sas[0].Children = []ChildSA{newChild("other", "INSTALLED")}
	}, map[string]string{
		"CHILD SA": "FAIL  CHILD SA  the IKE SA has no CHILD SA ikeward",
		"route":    noRange + "no CHILD SA is up",
	}},
	{"no address from the server", func(f fakes) {
		f.tunnel.sas[0].VIPs = []string{"fd00::1", "nonsense"}
	}, map[string]string{
		"IKE SA": "ok    IKE SA    ikeward is ESTABLISHED",
		"address": "FAIL  address   no address from the server to look for on ikeward0: " +
			"the server assigned none",
	}},
	{"mapped address", func(f fakes) {
		f.tunnel.sas[0].VIPs = []string{"fd00::1", "::ffff:203.0.113.130"}
	}, nil},
	// The address check names the IPv4 addresses alone, not ikeward0's own.
	{"address gone", func(f fakes) {
		f.network.addrs["ikeward0"] = []netip.Prefix{own}
	}, map[string]string{
		"address": "FAIL  address   203.0.113.130 is not on ikeward0, which has no IPv4 address",
	}},
	{"other addresses", func(f fakes) {
		f.network.addrs["ikeward0"] = []netip.Prefix{netip.MustParsePrefix("203.0.113.7/32"), own,
			netip.MustParsePrefix("10.0.0.1/8")}
	}, map[string]string{
		"address": "FAIL  address   203.0.113.130 is not on ikeward0, which has 203.0.113.7/32, " +
			"10.0.0.1/8",
	}},
	{"no link", func(f fakes) {
		f.network.addrsErr = errors.New("there is no such link")
	}, map[string]string{
		"address": "FAIL  address   cannot read the addresses of ikeward0: there is no such link",
		"DNS":     "FAIL  DNS       ikeward0: cannot read its addresses: there is no such link",
	}},
	{"no rule", func(f fakes) {
		f.network.rules = slices.Delete(slices.Clone(capturedRules), 2, 3)
	}, map[string]string{
		"rule": "FAIL  rule      no rule 32001: from all lookup 220 suppress_prefixlength 0",
	}},
	// strongSwan's rule as shipped, which looks up table 220 before main.
	{"strongSwan's rule", func(f fakes) {
		f.network.rules = []routing.Rule{plain(0, 255), plain(220, 220), plain(32766, 254),
			plain(32767, 253)}
	}, map[string]string{
		"rule": "FAIL  rule      no rule 32000: from all lookup main suppress_prefixlength 0; " +
			"no rule 32001: from all lookup 220 suppress_prefixlength 0; 220: from all lookup " +
			"220 looks up table 220 too, and is not ikeward's",
	}},
	{"rules unread", func(f fakes) {
		f.network.rulesErr = errors.New("operation not permitted")
	}, map[string]string{
		"rule": "FAIL  rule      cannot read the rules: operation not permitted",
	}},
	// A route of table 220 through another link.
	{"route through another link", func(f fakes) {
		f.network.routes[220] = []Route{{Dst: netip.MustParsePrefix("10.0.0.0/8"), Link: "ikeward0",
			Type: Unicast}, {Dst: netip.MustParsePrefix("10.88.0.0/16"), Link: "eth0",
			Type: Unicast}, floor}
	}, map[string]string{"route": noRoute + ", but one through eth0"}},
	{"route of another type", func(f fakes) {
		f.network.routes[220] = []Route{{Dst: netip.MustParsePrefix("10.88.0.0/16"),
			Link: "ikeward0", Type: "blackhole"}, floor}
	}, map[string]string{"route": noRoute}},
	{"other routes", func(f fakes) {
		f.network.routes[220] = []Route{{Dst: netip.MustParsePrefix("10.88.0.0/17"),
			Link: "ikeward0", Type: Unicast}, floor}
	}, map[string]string{"route": noRoute}},
	{"routes unread", func(f fakes) {
		f.network.routesErr = errors.New("operation not permitted")
	}, map[string]string{
		"route": "FAIL  route     cannot read table 220: operation not permitted",
		"floor": "FAIL  floor     cannot read table 220: operation not permitted",
	}},
	// As after ip route flush table 220, with the tunnel up: the route through ikeward0 alone.
	{"no floor", func(f fakes) {
		f.network.routes[220] = []Route{through}
	}, map[string]string{"floor": noFloor}},
	// The floor must be unreachable, which tells the sender at once, and at its own metric, which
	// updown's replace of its route leaves alone.
	{"floor otherwise", func(f fakes) {
		other := floor
		other.Metric = 0
		blackhole := floor
		blackhole.Type = "blackhole"
		wider := floor
		wider.Dst = netip.MustParsePrefix("192.0.0.0/8")
		f.network.routes[220] = []Route{through, other, blackhole, wider}
	}, map[string]string{"floor": noFloor}},
	{"resolver silent", func(f fakes) {
		f.resolver.err = errors.New("no answer in time")
	}, map[string]string{
		"resolver": "FAIL  resolver  192.0.2.10 gives no address for example.test: no answer in time",
	}},
	// An answer, if with an error code or no address, came through the tunnel.
	{"resolver NXDOMAIN", func(f fakes) {
		f.resolver.err = fmt.Errorf("%w NXDOMAIN", ErrAnswer)
	}, map[string]string{
		"resolver": "FAIL  resolver  192.0.2.10 gives no address for example.test: it answers " +
			"NXDOMAIN through the tunnel",
	}},
	{"resolver without an address", func(f fakes) {
		f.resolver.err = fmt.Errorf("%w with no address", ErrAnswer)
	}, map[string]string{
		"resolver": "FAIL  resolver  192.0.2.10 gives no address for example.test: it answers " +
			"with no address through the tunnel",
	}},
}

// ownGone is the DNS check's line where ikeward0 has lost its own address.
const ownGone = "FAIL  DNS       ikeward0: its own address, fd22:851f:35dd::1/128, is missing: " +
	"while the tunnel is down, the remote network's names go to the uplink's resolver, as " +
	"docs/status.md says"

// noRoute is the route check's line where table 220 does not route the remote network through
// ikeward0. Its line starts with noRange where no CHILD SA is up to say what the server granted.
const (
	noRoute = "FAIL  route     table 220 has no route to 10.88.0.0/16 through ikeward0"
	noRange = "FAIL  route     no range of the server's to look for in table 220: "
)

// noFloor is the floor check's line where table 220 lacks the floor.
const noFloor = "FAIL  floor     table 220 has no unreachable route to 10.88.0.0/16 at metric " +
	"4000: while the tunnel is down, traffic there takes the default route; learned: 10.88.0.0/16"

// Each check fails, alone, for what it finds wrong, and says what that is.
func TestUnhealthy(t *testing.T) {
	checkChanges(t, cases)
}
