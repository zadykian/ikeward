package status

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/zadykian/ikeward/internal/files"
)

// resolvedLink is what resolvectl --json=short status LINK says of a link, as systemd 259 has it.
type resolvedLink struct {
	Name string `json:"ifname"`
	// DefaultRoute is whether lookups of any name may go to the link's servers.
	DefaultRoute bool             `json:"defaultRoute"`
	Servers      []resolvedServer `json:"servers"`
	Domains      []resolvedDomain `json:"searchDomains"`
	// Scopes are those resolved looks up names in: one of protocol dns once the link may carry
	// lookups, which takes an address on it.
	Scopes []resolvedScope `json:"scopes"`
}

// resolvedServer is a DNS server of a link.
type resolvedServer struct {
	Address string `json:"addressString"`
}

// resolvedDomain is a domain of a link: with RouteOnly, a routing domain, ~NAME, which sends
// lookups of its names to the link's servers but is no search domain.
type resolvedDomain struct {
	Name      string `json:"name"`
	RouteOnly bool   `json:"routeOnly"`
}

// resolvedScope is a protocol that resolved looks names up with on a link.
type resolvedScope struct {
	Protocol string `json:"protocol"`
}

// DNSCheck names the check of resolved's settings of files.Link and of its own address, which
// networkd gives. A restart of strongSwan does not mend its failure, as the watchdog's unmendable
// says.
const DNSCheck = "DNS"

// CheckDNS is the DNS check: resolved sends lookups of the settings' domains, and only those, on
// files.Link to their resolver, as networkd sets it up. The link's own address, files.LinkULA,
// must be on it too, whether the tunnel is up or not: it keeps those lookups there while the
// tunnel is down.
func (s System) CheckDNS(settings files.Settings) Result {
	r := Result{Name: DNSCheck}
	args := []string{"--json=short", "status", files.Link}
	out, err := s.Runner.Run("resolvectl", args...)
	if err != nil {
		r.Detail = fmt.Sprintf("resolvectl %s: %v", strings.Join(args, " "), err)
		return r
	}
	var links []resolvedLink
	if err := json.Unmarshal([]byte(out), &links); err != nil {
		r.Detail = "cannot read what resolvectl printed: " + err.Error()
		return r
	}
	i := slices.IndexFunc(links, func(l resolvedLink) bool { return l.Name == files.Link })
	if i < 0 {
		r.Detail = "resolvectl says nothing of " + files.Link
		return r
	}
	wrong := links[i].wrong(settings)
	if missing := s.ownAddress(); missing != "" {
		wrong = append(wrong, missing)
	}
	if len(wrong) > 0 {
		r.Detail = files.Link + ": " + strings.Join(wrong, "; ")
		return r
	}
	r.OK = true
	r.Detail = fmt.Sprintf("resolved sends %s lookups on %s to %s",
		strings.Join(settings.RoutingDomains(), " "), files.Link, settings.DNS)
	return r
}

// wrong says what in l's settings differs from what networkd sets from settings.
func (l resolvedLink) wrong(settings files.Settings) []string {
	var wrong []string
	if !slices.ContainsFunc(l.Servers, func(s resolvedServer) bool {
		return s.Address == settings.DNS.String()
	}) {
		wrong = append(wrong, settings.DNS.String()+" is not among its DNS servers")
	}
	var domains []string
	for _, d := range l.Domains {
		if d.RouteOnly {
			domains = append(domains, "~"+d.Name)
		} else {
			domains = append(domains, d.Name)
		}
	}
	want := settings.RoutingDomains()
	for _, d := range want {
		if !slices.Contains(domains, d) {
			wrong = append(wrong, "it has no domain "+d)
		}
	}
	for _, d := range domains {
		if !slices.Contains(want, d) {
			wrong = append(wrong, "it has the domain "+d+" too")
		}
	}
	if l.DefaultRoute {
		wrong = append(wrong, "lookups of any name may go to it")
	}
	if !slices.ContainsFunc(l.Scopes, func(s resolvedScope) bool { return s.Protocol == "dns" }) {
		wrong = append(wrong, "resolved sends it no lookup")
	}
	return wrong
}

// ownAddress says that files.LinkULA is not on files.Link, and what follows, or nothing where it
// is. Without it, resolved takes the link's DNS scope away as strongSwan's address goes. How to
// put it back, and where networkd cannot, is in docs/status.md.
func (s System) ownAddress() string {
	prefixes, err := s.Network.Addrs(files.Link)
	if err != nil {
		return "cannot read its addresses: " + err.Error()
	}
	own := netip.MustParsePrefix(files.LinkULA).Addr()
	if slices.ContainsFunc(prefixes, func(p netip.Prefix) bool { return p.Addr() == own }) {
		return ""
	}
	return "its own address, " + files.LinkULA + ", is missing: while the tunnel is down, the " +
		"remote network's names go to the uplink's resolver, as docs/status.md says"
}
