package files

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
)

// SettingsFile holds what install's flags give, as Generate writes it. ikeward routing, status and
// the watchdog read it.
const SettingsFile = "/var/lib/ikeward/settings"

// Settings are the remote network's values, which install's flags give and SettingsFile holds.
type Settings struct {
	// DNS is the remote network's resolver, an IPv4 address, which Link sends Domains' names to.
	DNS netip.Addr
	// Domains are the remote network's domains, without the ~ of a routing domain: Link sends
	// lookups of names under them to DNS alone, and no other lookup.
	Domains []string
	// Probe is a name DNS answers, which status asks it for.
	Probe string
	// Floors are ranges that keep a floor from boot on, whatever ikeward updown learns.
	Floors []netip.Prefix
}

// domain is a domain name as Settings takes it: labels of lowercase letters, digits and hyphens,
// joined by dots, with no dot at its end.
var domain = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}` +
	`[a-z0-9])?)*$`)

// maxDomain is the longest domain name, in its written form, as RFC 1035 allows.
const maxDomain = 253

// Check says what in s is wrong, or nothing, each fault once. The resolver must be an IPv4 unicast
// address. Each domain name and floor must be right, and given once.
func (s Settings) Check() error {
	var errs []error
	if !s.DNS.Is4() || !s.DNS.IsGlobalUnicast() {
		errs = append(errs, fmt.Errorf("the resolver, %s, is no IPv4 unicast address", s.DNS))
	}
	if len(s.Domains) == 0 {
		errs = append(errs, errors.New("no domain"))
	}
	for i, d := range s.Domains {
		switch {
		case !isDomain(d):
			errs = append(errs, fmt.Errorf("the domain %q is no domain name", d))
		case slices.Contains(s.Domains[:i], d):
			errs = append(errs, fmt.Errorf("the domain %s comes twice", d))
		}
	}
	// A probe that is one of the domains, as install's default, was checked as a domain.
	if !slices.Contains(s.Domains, s.Probe) && !isDomain(s.Probe) {
		errs = append(errs, fmt.Errorf("the probe, %q, is no domain name", s.Probe))
	}
	for i, p := range s.Floors {
		switch {
		case !p.IsValid() || !p.Addr().Is4():
			errs = append(errs, fmt.Errorf("the floor %s is no IPv4 range", p))
		case p != p.Masked():
			errs = append(errs, fmt.Errorf("the floor %s is no range's first address: %s is", p,
				p.Masked()))
		case slices.Contains(s.Floors[:i], p):
			errs = append(errs, fmt.Errorf("the floor %s comes twice", p))
		}
	}
	return errors.Join(errs...)
}

// isDomain is whether name is a domain name as Settings takes it.
func isDomain(name string) bool {
	return len(name) <= maxDomain && domain.MatchString(name)
}

// RoutingDomains are s's domains as networkd's Domains= takes them, each a routing domain, ~NAME.
// Such a domain sends lookups of its names to the link's servers, but is no search domain.
func (s Settings) RoutingDomains() []string {
	routing := make([]string, len(s.Domains))
	for i, d := range s.Domains {
		routing[i] = "~" + d
	}
	return routing
}

// ParseSettings reads data as Generate writes SettingsFile: a key and its value on each line. A
// key it does not know, a value it cannot read, a resolver or probe given twice, or settings that
// Check refuses fail it.
func ParseSettings(data []byte) (Settings, error) {
	var s Settings
	probes := 0
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		var err error
		switch key {
		case "dns":
			if s.DNS.IsValid() {
				return Settings{}, fmt.Errorf("line %d: a second resolver", i+1)
			}
			s.DNS, err = netip.ParseAddr(value)
		case "domain":
			s.Domains = append(s.Domains, value)
		case "probe":
			probes++
			if probes > 1 {
				return Settings{}, fmt.Errorf("line %d: a second probe", i+1)
			}
			s.Probe = value
		case "floor":
			var p netip.Prefix
			p, err = netip.ParsePrefix(value)
			s.Floors = append(s.Floors, p)
		default:
			return Settings{}, fmt.Errorf("line %d: %q is no setting", i+1, line)
		}
		if err != nil {
			return Settings{}, fmt.Errorf("line %d: %q: %w", i+1, line, err)
		}
	}
	if err := s.Check(); err != nil {
		return Settings{}, err
	}
	return s, nil
}
