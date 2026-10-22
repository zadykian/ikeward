package host

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

// Names resolves host names through the system's resolver: Go's reads /etc/hosts, then asks the
// DNS servers that /etc/resolv.conf names.
type Names struct {
	// lookup is net.DefaultResolver's LookupNetIP where nil. Tests give a fake.
	lookup func(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// IPv4 returns name's IPv4 addresses, at least one, or an error that says why there is none.
func (n Names) IPv4(ctx context.Context, name string) ([]netip.Addr, error) {
	lookup := n.lookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupNetIP
	}
	addrs, err := lookup(ctx, "ip4", name)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%s has no IPv4 address", name)
	}
	for i, a := range addrs {
		addrs[i] = a.Unmap()
	}
	return addrs, nil
}
