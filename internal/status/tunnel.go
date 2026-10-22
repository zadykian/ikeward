package status

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/zadykian/ikeward/internal/files"
)

// The states of an SA that is up. While strongSwan rekeys an SA, the SA's state is REKEYING, and
// it still carries traffic, until its replacement is up.
const (
	established = "ESTABLISHED"
	installed   = "INSTALLED"
	rekeying    = "REKEYING"
)

// tunnel is what strongSwan says of the connection.
type tunnel struct {
	sas []IKESA
	// err is why strongSwan could not say.
	err error
	// up is the IKE SA that is up, if any, and vip the IPv4 address the server assigned it.
	up  *IKESA
	vip netip.Addr
}

// newTunnel is the tunnel that sas, or err, give. While an SA is being replaced, two may be up:
// up is then one whose CHILD SA is up.
func newTunnel(sas []IKESA, err error) tunnel {
	if err != nil {
		return tunnel{err: err}
	}
	t := tunnel{sas: sas}
	for i := range sas {
		if (sas[i].State == established || sas[i].State == rekeying) &&
			(t.up == nil || child(*t.up) == nil) {
			t.up = &sas[i]
		}
	}
	if t.up != nil {
		for _, v := range t.up.VIPs {
			if a, err := netip.ParseAddr(v); err == nil && a.Unmap().Is4() {
				t.vip = a.Unmap()
				break
			}
		}
	}
	return t
}

// Grants is whether an IKE SA of sas, up with its CHILD SA of the connection, grants dst: whether
// dst is one of that CHILD SA's Ranges. Up means what the IKE SA and CHILD SA checks take it to
// mean.
func Grants(sas []IKESA, dst netip.Prefix) bool {
	for _, sa := range sas {
		if sa.State != established && sa.State != rekeying {
			continue
		}
		if c := child(sa); c != nil && slices.Contains(Ranges(c.RemoteTS), dst) {
			return true
		}
	}
	return false
}

// Ranges are the IPv4 ranges of traffic selectors as strongSwan lists them, once each, in order.
// Each is the subnet strongSwan gives updown in PLUTO_PEER_CLIENT for it: the selector's own, or
// the least that holds its range. Ranges passes over any other selector, as an IPv6 one.
func Ranges(selectors []string) []netip.Prefix {
	var ranges []netip.Prefix
	for _, ts := range selectors {
		if p, ok := subnet(ts); ok && !slices.Contains(ranges, p) {
			ranges = append(ranges, p)
		}
	}
	return ranges
}

// subnet is the IPv4 subnet of ts, which strongSwan prints as a subnet or as two addresses joined
// by "..", either followed by a protocol and port in brackets.
func subnet(ts string) (netip.Prefix, bool) {
	ts, _, _ = strings.Cut(ts, "[")
	from, to, isRange := strings.Cut(ts, "..")
	if !isRange {
		p, err := netip.ParsePrefix(ts)
		return p.Masked(), err == nil && p.Addr().Is4()
	}
	first, err := netip.ParseAddr(from)
	last, lastErr := netip.ParseAddr(to)
	if err != nil || lastErr != nil || !first.Is4() || !last.Is4() {
		return netip.Prefix{}, false
	}
	// The bits both ends share, as strongSwan's to_subnet takes them; /0 holds any two.
	for bits := 32; ; bits-- {
		if p := netip.PrefixFrom(first, bits).Masked(); p.Contains(last) {
			return p, true
		}
	}
}

// child is sa's CHILD SA of the connection that is up, if any: one INSTALLED, else one REKEYING,
// whose kernel SAs stay in place until its replacement is installed.
func child(sa IKESA) *ChildSA {
	for _, state := range []string{installed, rekeying} {
		for i, c := range sa.Children {
			if c.Name == files.Connection && c.State == state {
				return &sa.Children[i]
			}
		}
	}
	return nil
}

// ikeSA is the IKE SA check: strongSwan has an IKE SA of the connection, and that SA is up, or
// rekeying.
func (t tunnel) ikeSA() Result {
	r := Result{Name: "IKE SA"}
	switch {
	case t.err != nil:
		r.Detail = "cannot ask strongSwan: " + t.err.Error()
	case len(t.sas) == 0:
		r.Detail = "strongSwan has no IKE SA of " + files.Connection
	case t.up == nil:
		var states []string
		for _, sa := range t.sas {
			states = append(states, sa.State)
		}
		r.Detail = files.Connection + " is " + strings.Join(states, ", ")
	default:
		r.OK = true
		r.Detail = files.Connection + " is " + t.up.State
		if t.vip.IsValid() {
			r.Detail += ", with " + t.vip.String() + " from the server"
		}
	}
	return r
}

// childSA is the CHILD SA check: the IKE SA that is up has the connection's child installed, or
// rekeying.
func (t tunnel) childSA() Result {
	r := Result{Name: "CHILD SA"}
	switch {
	case t.err != nil:
		r.Detail = "not known, as strongSwan could not be asked"
		return r
	case t.up == nil:
		r.Detail = "no IKE SA of " + files.Connection + " is up to carry it"
		return r
	}
	if c := child(*t.up); c != nil {
		r.OK = true
		r.Detail = files.Connection + " is " + c.State + ": " + strings.Join(c.LocalTS, ", ") +
			" === " + strings.Join(c.RemoteTS, ", ")
		return r
	}
	var states []string
	for _, c := range t.up.Children {
		if c.Name == files.Connection && !slices.Contains(states, c.State) {
			states = append(states, c.State)
		}
	}
	if len(states) == 0 {
		r.Detail = "the IKE SA has no CHILD SA " + files.Connection
	} else {
		r.Detail = files.Connection + " is " + strings.Join(states, ", ")
	}
	return r
}

// noAddress says why the server's address is not known.
func (t tunnel) noAddress() string {
	switch {
	case t.err != nil:
		return "strongSwan could not be asked"
	case t.up == nil:
		return "no IKE SA is up"
	}
	return "the server assigned none"
}

// child is the up IKE SA's CHILD SA of the connection that is up, if any.
func (t tunnel) child() *ChildSA {
	if t.up == nil {
		return nil
	}
	return child(*t.up)
}

// noChild says why no CHILD SA is up: strongSwan could not be asked, or no IKE SA or CHILD SA is
// up.
func (t tunnel) noChild() string {
	if t.err != nil || t.up == nil {
		return t.noAddress()
	}
	return "no CHILD SA is up"
}
