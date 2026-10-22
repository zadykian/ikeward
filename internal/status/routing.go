package status

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// RuleCheck names the check of the rules, which ikeward routing makes. A restart of strongSwan
// does not mend it, and the watchdog makes them again.
const RuleCheck = "rule"

// rule is the rule check: the rules hold routing.Rules, the server's own networks before the
// remote network's. No other rule looks up files.Table, as one before them would put the remote
// network first. Without the rules, the remote network's traffic would take the default route.
func (s System) rule() Result {
	r := Result{Name: RuleCheck}
	rules, err := s.Network.Rules()
	if err != nil {
		r.Detail = "cannot read the rules: " + err.Error()
		return r
	}
	var wrong []string
	for _, rule := range routing.Rules {
		if !slices.Contains(rules, rule) {
			wrong = append(wrong, "no rule "+rule.String())
		}
	}
	for _, rule := range routing.Foreign(rules) {
		wrong = append(wrong, fmt.Sprintf("%s looks up table %d too, and is not ikeward's", rule,
			files.Table))
	}
	if len(wrong) > 0 {
		r.Detail = strings.Join(wrong, "; ")
		return r
	}
	r.OK = true
	r.Detail = fmt.Sprintf("%d looks up main, then %d table %d, both with "+
		"suppress_prefixlength 0", routing.Rules[0].Priority, routing.Rules[1].Priority, files.Table)
	return r
}

// route is the route check: files.Table routes each range of the up CHILD SA's remote traffic
// selectors through files.Link, the tunnel's link, as updown sets them. What leaves by it takes
// the address on it, the one the server assigned. The kernel removes the routes as that address
// goes, so they need the tunnel up.
func (s System) route(t tunnel) Result {
	r := Result{Name: "route"}
	c := t.child()
	if c == nil {
		r.Detail = fmt.Sprintf("no range of the server's to look for in table %d: %s", files.Table,
			t.noChild())
		return r
	}
	granted, refused := t.routed(Ranges(c.RemoteTS))
	if len(granted) == 0 {
		named := c.RemoteTS
		if len(refused) > 0 {
			named = refused
		}
		r.Detail = "the CHILD SA names no IPv4 range of the server's that ikeward routes: " +
			strings.Join(named, ", ")
		return r
	}
	routes, err := s.Network.Routes(files.Table)
	if err != nil {
		r.Detail = fmt.Sprintf("cannot read table %d: %v", files.Table, err)
		return r
	}
	var missing []netip.Prefix
	var others []string
	for _, dst := range granted {
		if links := unicast(routes, dst); !slices.Contains(links, files.Link) {
			missing = append(missing, dst)
			others = append(others, links...)
		}
	}
	if len(missing) == 0 {
		r.OK = true
		r.Detail = fmt.Sprintf("table %d routes %s through %s", files.Table, join(granted),
			files.Link)
		if len(refused) > 0 {
			r.Detail += "; refused: " + strings.Join(refused, ", ")
		}
		return r
	}
	r.Detail = fmt.Sprintf("table %d has no route to %s through %s", files.Table, join(missing),
		files.Link)
	if others = slices.Compact(slices.Sorted(slices.Values(others))); len(others) > 0 {
		r.Detail += ", but one through " + strings.Join(others, ", ")
	}
	return r
}

// routed splits ranges into those updown routes and those routing.Refusal refuses, with the VPN
// server's address, each of these with why.
func (t tunnel) routed(ranges []netip.Prefix) (routed []netip.Prefix, refused []string) {
	var servers []netip.Addr
	if a, err := netip.ParseAddr(t.up.Remote); err == nil {
		servers = append(servers, a)
	}
	for _, dst := range ranges {
		if why := routing.Refusal(dst, servers); why != "" {
			refused = append(refused, fmt.Sprintf("%s (%s)", dst, why))
		} else {
			routed = append(routed, dst)
		}
	}
	return routed, refused
}

// unicast are the links of the unicast routes of routes to dst that name one.
func unicast(routes []Route, dst netip.Prefix) []string {
	var links []string
	for _, route := range routes {
		if route.Dst == dst && route.Type == Unicast && route.Link != "" {
			links = append(links, route.Link)
		}
	}
	return links
}

// FloorCheck names the check of the floors, which ikeward routing makes. A restart of strongSwan
// does not mend them, and the watchdog makes them again.
const FloorCheck = "floor"

// floor is the floor check: at routing.FloorMetric, files.Table makes each of the Floors
// unreachable, whether the tunnel is up or not. Those are the settings' and each range updown
// learned. While no route through files.Link takes them, the floors keep their traffic off the
// default route. The line ends with the ranges routing.Learned records, and those passed over.
func (s System) floor(settings files.Settings) Result {
	r := Result{Name: FloorCheck}
	learned, err := routing.ReadLearned(s.Files)
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	routes, err := s.Network.Routes(files.Table)
	if err != nil {
		r.Detail = fmt.Sprintf("cannot read table %d: %v", files.Table, err)
		return r
	}
	floors, passed := learned.Floors(settings.Floors)
	var missing []netip.Prefix
	for _, dst := range floors {
		if !slices.Contains(routes, Route{Dst: dst, Type: Unreachable, Metric: routing.FloorMetric}) {
			missing = append(missing, dst)
		}
	}
	var kept, over []string
	for _, p := range learned.Ranges {
		if !slices.ContainsFunc(passed, func(q routing.Passed) bool { return q.Range == p }) {
			kept = append(kept, p.String())
		}
	}
	for _, p := range passed {
		over = append(over, fmt.Sprintf("%s (%s)", p.Range, p.Why))
	}
	known := "; learned: none yet"
	if len(kept) > 0 {
		known = "; learned: " + strings.Join(kept, ", ")
	}
	if len(over) > 0 {
		known += "; passed over: " + strings.Join(over, ", ")
	}
	if len(missing) > 0 {
		r.Detail = fmt.Sprintf("table %d has no %s route to %s at metric %d: while the tunnel is "+
			"down, traffic there takes the default route%s", files.Table, Unreachable, join(missing),
			routing.FloorMetric, known)
		return r
	}
	r.OK = true
	r.Detail = fmt.Sprintf("table %d holds %s %s metric %d: while the tunnel is down, traffic "+
		"there goes nowhere%s", files.Table, Unreachable, join(floors), routing.FloorMetric, known)
	if len(floors) == 0 {
		r.Detail = fmt.Sprintf("no floor to look for in table %d%s", files.Table, known)
	}
	return r
}
