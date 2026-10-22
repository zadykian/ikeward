package status

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// granted are the ranges the integration test's server grants: the remote network, and a second
// range.
var granted = []string{"192.0.2.0/24", "198.18.0.0/24"}

// via is updown's route to dst through ikeward0, and floorOf the floor of dst.
func via(dst string) Route {
	return Route{Dst: netip.MustParsePrefix(dst), Link: "ikeward0", Type: Unicast}
}

func floorOf(dst string) Route {
	return Route{Dst: netip.MustParsePrefix(dst), Type: Unreachable, Metric: 4000}
}

// twoRanges has the healthy fakes grant granted, as updown learned them: table 220 routes both
// through ikeward0, and holds their floors and the settings' floor.
func twoRanges(f fakes) {
	f.tunnel.sas[0].Children[0].RemoteTS = granted
	f.files.files[routing.Learned] = granted[0] + "\n" + granted[1] + "\n"
	f.network.routes[220] = []Route{via(granted[0]), via(granted[1]), floor, floorOf(granted[0]),
		floorOf(granted[1])}
}

// Why ikeward refuses a range wider than /8, and one that holds the VPN server's address.
const (
	wide  = "wider than /8, it would take the server's own traffic"
	holds = "it holds 198.51.100.10, the VPN server's address"
)

// The lines of the CHILD SA, route and floor checks, where the server grants two ranges.
const (
	twoChild = "ok    CHILD SA  ikeward is INSTALLED: 203.0.113.130/32 === 192.0.2.0/24, " +
		"198.18.0.0/24"
	twoRoutes = "ok    route     table 220 routes 192.0.2.0/24, 198.18.0.0/24 through ikeward0"
	twoFloors = "ok    floor     table 220 holds unreachable 10.88.0.0/16, 192.0.2.0/24, " +
		"198.18.0.0/24 metric 4000: while the tunnel is down, traffic there goes nowhere; " +
		"learned: 192.0.2.0/24, 198.18.0.0/24"
)

// rangeCases change the healthy fakes, as the server grants other ranges, and as updown learned
// them.
var rangeCases = []change{
	{"two ranges", twoRanges, map[string]string{"CHILD SA": twoChild, "route": twoRoutes,
		"floor": twoFloors}},
	{"a route missing", func(f fakes) {
		twoRanges(f)
		f.network.routes[220] = f.network.routes[220][1:]
	}, map[string]string{"CHILD SA": twoChild, "route": "FAIL  route     table 220 has no " +
		"route to 192.0.2.0/24 through ikeward0", "floor": twoFloors}},
	{"a floor missing", func(f fakes) {
		twoRanges(f)
		f.network.routes[220] = f.network.routes[220][:4]
	}, map[string]string{"CHILD SA": twoChild, "route": twoRoutes,
		"floor": "FAIL  floor     table 220 has no unreachable route to 198.18.0.0/24 at " +
			"metric 4000: while the tunnel is down, traffic there takes the default route; " +
			"learned: 192.0.2.0/24, 198.18.0.0/24"}},
	// Before the tunnel first came up, as on a fresh install: the settings' floor alone.
	{"none learned", func(f fakes) {
		delete(f.files.files, routing.Learned)
	}, map[string]string{"floor": "ok    floor     table 220 holds unreachable " +
		"10.88.0.0/16 metric 4000: while the tunnel is down, traffic there goes nowhere; " +
		"learned: none yet"}},
	// A grant of every address is refused, and so is one that holds the VPN server's
	// address. A record that holds either, as by hand, has no floor for it.
	{"every address", func(f fakes) {
		f.tunnel.sas[0].Children[0].RemoteTS = []string{"0.0.0.0/0"}
		f.files.files[routing.Learned] = "0.0.0.0/0\n"
		f.network.routes[220] = []Route{floor}
	}, map[string]string{
		"CHILD SA": "ok    CHILD SA  ikeward is INSTALLED: 203.0.113.130/32 === 0.0.0.0/0",
		"route": "FAIL  route     the CHILD SA names no IPv4 range of the server's that " +
			"ikeward routes: 0.0.0.0/0 (" + wide + ")",
		"floor": "ok    floor     table 220 holds unreachable 10.88.0.0/16 metric 4000: while " +
			"the tunnel is down, traffic there goes nowhere; learned: none yet; passed over: " +
			"0.0.0.0/0 (" + wide + ")"}},
	{"the server's range", func(f fakes) {
		f.tunnel.sas[0].Children[0].RemoteTS = []string{"198.51.100.0/24", "198.51.101.0/24"}
		f.files.files[routing.Learned] = "server 198.51.100.10\n198.51.100.0/24\n" +
			"198.51.101.0/24\n"
		f.network.routes[220] = []Route{via("198.51.101.0/24"), floor,
			floorOf("198.51.101.0/24")}
	}, map[string]string{
		"CHILD SA": "ok    CHILD SA  ikeward is INSTALLED: 203.0.113.130/32 === " +
			"198.51.100.0/24, 198.51.101.0/24",
		"route": "ok    route     table 220 routes 198.51.101.0/24 through ikeward0; refused: " +
			"198.51.100.0/24 (" + holds + ")",
		"floor": "ok    floor     table 220 holds unreachable 10.88.0.0/16, 198.51.101.0/24 " +
			"metric 4000: while the tunnel is down, traffic there goes nowhere; learned: " +
			"198.51.101.0/24; passed over: 198.51.100.0/24 (" + holds + ")"}},
	// With no floor in the settings, and none learned yet, there is none to look for.
	{"no floor", func(f fakes) {
		delete(f.files.files, routing.Learned)
		f.files.files[files.SettingsFile] = strings.TrimSuffix(installedSettings,
			"floor 10.88.0.0/16\n")
	}, map[string]string{"floor": "ok    floor     no floor to look for in table 220; " +
		"learned: none yet"}},
	// A floor of the settings that holds the VPN server's address, as one given before the
	// server moved, is passed over too.
	{"the server's floor", func(f fakes) {
		f.files.files[routing.Learned] = "server 198.51.100.10\n10.88.0.0/16\n"
		f.files.files[files.SettingsFile] += "floor 198.51.0.0/16\n"
	}, map[string]string{"floor": "ok    floor     table 220 holds unreachable 10.88.0.0/16 " +
		"metric 4000: while the tunnel is down, traffic there goes nowhere; learned: " +
		"10.88.0.0/16; passed over: 198.51.0.0/16 (" + holds + ")"}},
	{"IPv6 alone", func(f fakes) {
		f.tunnel.sas[0].Children[0].RemoteTS = []string{"fd00::/8"}
	}, map[string]string{
		"CHILD SA": "ok    CHILD SA  ikeward is INSTALLED: 203.0.113.130/32 === fd00::/8",
		"route": "FAIL  route     the CHILD SA names no IPv4 range of the server's that " +
			"ikeward routes: fd00::/8"}},
	{"record unread", func(f fakes) {
		f.files.errPath, f.files.err = routing.Learned, errors.New("input/output error")
	}, map[string]string{"floor": "FAIL  floor     cannot read " + routing.Learned +
		": input/output error"}},
	{"record broken", func(f fakes) {
		f.files.files[routing.Learned] = "nonsense\n"
	}, map[string]string{"floor": "FAIL  floor     " + routing.Learned + `: line 1: "nonsense" ` +
		"is no IPv4 range"}},
}

// The route check wants a route through ikeward0 for each range the CHILD SA grants that ikeward
// routes. The floor check wants a floor for the settings' range and each learned range. Their
// lines name those learned, and those refused.
func TestLearnedRanges(t *testing.T) {
	checkChanges(t, rangeCases)
}
