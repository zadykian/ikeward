package routing

import (
	"fmt"
	"net/netip"
)

// MinBits is the shortest prefix of a range that ikeward routes through the tunnel: /8, as of
// 10.0.0.0/8, the widest private block. Rule 32001 passes over a route to 0.0.0.0/0 alone. A wider
// grant, as 0.0.0.0/1 with 128.0.0.0/1, would take the server's traffic to the Internet into the
// tunnel. Its floors would cut that traffic off while the tunnel is down.
const MinBits = 8

// Refusal says why ikeward neither routes dst through the tunnel nor makes a floor of it, or
// nothing where it may. dst is wider than MinBits, or holds an address of servers, the VPN
// server's. A route there would take IKE's and ESP's own packets into the tunnel, and a floor
// would keep the tunnel from coming up again, from boot on.
func Refusal(dst netip.Prefix, servers []netip.Addr) string {
	if dst.Bits() < MinBits {
		return fmt.Sprintf("wider than /%d, it would take the server's own traffic", MinBits)
	}
	for _, a := range servers {
		if dst.Contains(a.Unmap()) {
			return fmt.Sprintf("it holds %s, the VPN server's address", a.Unmap())
		}
	}
	return ""
}
