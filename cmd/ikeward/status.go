package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/host"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// statusLong is a variable, as it shows the floors' metric from routing.
var statusLong = `Check the VPN as the watchdog does, and change nothing. status
prints a line per check, ok or FAIL, with what it found:

  - IKE SA: strongSwan's IKE SA ` + files.Connection + ` is ESTABLISHED, which strongSwan
    says through its VICI socket, ` + host.VICISocket + `;
  - CHILD SA: that SA's CHILD SA ` + files.Connection + ` is INSTALLED;
  - address: the address the server assigned is on the link ` + files.Link + `, the
    tunnel's;
  - rule: ikeward's rules look up main, then table 220, each but for its
    default route, and no other rule looks up table 220, as routing --help
    says;
  - route: table 220 routes each range the CHILD SA names on the server's side,
    those the VPN server granted, through ` + files.Link + `, as updown sets them.
    The line names those updown refuses, as updown --help says;
  - resolver: the remote network's resolver, install's --dns, answers a query
    for --probe's name within 5 seconds, which shows the tunnel carries traffic;
    it is not asked where a check above fails. The query leaves by ` + files.Link + `
    alone, bound to it, so it goes through the tunnel or nowhere. It is sent
    again each second while no reply answers it, so one lost datagram fails
    nothing. An answer with an error code, as SERVFAIL, or with no address
    fails the check too, and its line says the answer came through the tunnel;
  - DNS: systemd-resolved sends lookups of the remote network's names, those of
    each --domain, and only those, on ` + files.Link + ` to that resolver, as resolvectl
    says; and ` + files.Link + ` holds its own address, ` + files.LinkULA + `, which
    keeps those lookups off the uplink's resolver while the tunnel is down;
  - floor: table 220 holds an unreachable route at metric ` + floorMetric + ` for each
    --floor, and the same for each range updown learned, which keep the remote
    network's traffic off the default route while the tunnel is down. The line
    ends with the ranges ` + routing.Learned + ` records, and
    those passed over;
  - NAT: the nftables table ip ` + routing.NATTable + ` holds its chain and its one rule,
    which masquerades what leaves by ` + files.Link + `. The line says how many
    connections took the rule since the table was made. Where the table is
    missing or not ikeward's, the line says what a forwarded network loses.

The resolver, DNS and floor checks read the remote network's values from
` + files.SettingsFile + `, which install writes. Without it, each fails, and
says why.

status exits 0 where every check passes, and 1 otherwise. Only root may reach
strongSwan's socket and read nftables.`

// newStatus is ikeward status.
func newStatus(sys system) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check the VPN, and change nothing",
		Long:  statusLong,
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			results := sys.status().Check(command.Context())
			for _, r := range results {
				if _, err := fmt.Fprintln(command.OutOrStdout(), r); err != nil {
					return err
				}
			}
			if failed := status.Failed(results); failed > 0 {
				return fmt.Errorf("%d of %d checks failed", failed, len(results))
			}
			return nil
		},
	}
}

// status is the checks' view of s.
func (s system) status() status.System {
	return status.System{Tunnel: s.tunnel, Network: s.network, Resolver: s.resolver,
		Runner: s.runner, Files: s.files, NAT: s.nat}
}
