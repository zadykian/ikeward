package status

import (
	"errors"
	"fmt"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// NATCheck names the check of routing.NATTable, which masquerades what leaves by files.Link. A
// restart of strongSwan does not mend it, and the watchdog makes the table again.
const NATCheck = "NAT"

// nat is the NAT check: routing.NATTable holds its chain and rule alone, and the line says how
// many connections took the rule. Only a routing.Missing says the rule is gone: another error, as
// nftables refusing a user other than root, leaves the table maybe in place.
func (s System) nat() Result {
	r := Result{Name: NATCheck}
	connections, err := s.NAT.Connections()
	if err != nil {
		r.Detail = err.Error()
		// Docker's MASQUERADE still gives a container's traffic the tunnel's address.
		if errors.As(err, &routing.Missing{}) {
			r.Detail += fmt.Sprintf(": a forwarded network's traffic leaves %s from its own "+
				"address, which no SA takes", files.Link)
		}
		return r
	}
	r.OK = true
	r.Detail = fmt.Sprintf("table ip %s masquerades what leaves by %s; connections so far: %d",
		routing.NATTable, files.Link, connections)
	return r
}
