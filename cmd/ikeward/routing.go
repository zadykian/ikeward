package main

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// routingLong is a variable, as it shows the rules as routing.Rule.String gives them.
var routingLong = `Make ikeward's routing, as root. Every packet, the server's own, a
container's and a forwarded network's, then follows one order: the server's
own networks, then the remote network's, then the default route. routing makes:

  - two rules, after the kernel's rule for the local table:

      ` + rule0 + `
      ` + rule1 + `

    The first looks up main, the server's own networks, but passes over its
    default route. The second looks up table ` + table + `, where updown routes each
    range the VPN server grants through ` + files.Link + `, the tunnel's link,
    while the tunnel is up. The kernel's rule for main, at 32766, then gives
    the default route;
  - the floors in table ` + table + `, at metric ` + floorMetric + `, unreachable routes: one ` +
	`for each
    range install's --floor gave, which ` + files.SettingsFile + ` holds, and
    one for each range updown learned, which ` + routing.Learned + `
    records. While the tunnel is down, no route through ` + files.Link + ` comes before
    them: a connection to the remote network fails at once, as unreachable,
    and nothing for it leaves by the uplink. routing passes over a range wider
    than /` + minBits + `, or one that holds an address of the VPN server's that
    ` + routing.Learned + ` records, as updown --help says, and
    names it;
  - the nftables table ip ` + routing.NATTable + `, whose one rule gives what leaves by
    ` + files.Link + ` the address the server assigned, whatever its source:

      ` + routing.NATRule + `

    Its chain hooks postrouting at priority 99, before Docker's NAT rules at
    100, so it decides first. A forwarded network, as WireGuard's, reaches the
    remote network through it, with no rule of its own.

routing adds a rule only where it is missing. It then removes any other rule
that looks up table ` + table + `, as strongSwan's did at priority 220 before ikeward
turned it off: that rule put the remote network before the server's own.
It makes the floors and the table anew, in place of what was there. Where a
part fails, it makes the others, then exits 1.

The unit ` + routing.Unit + ` runs routing as the server boots, before any
network comes up and before strongSwan, and install restarts it before
strongSwan starts. status's rule, floor and NAT checks read what it made, the
watchdog makes each again where its check fails, and uninstall removes them.

A network of the server's inside the remote network's, as a LAN inside a
floor, keeps its traffic, the tunnel up or down, as main comes first. A host
of the remote network's inside such a network is out of the server's reach.

To see them, and the ranges learned:

  ip rule
  ip route show table ` + table + `
  nft list table ip ` + routing.NATTable + `
  cat ` + routing.Learned

// The rules, the routing table and the floors' metric, as routingLong shows them.
var (
	rule0       = routing.Rules[0].String()
	rule1       = routing.Rules[1].String()
	table       = strconv.Itoa(files.Table)
	floorMetric = strconv.Itoa(routing.FloorMetric)
)

// newRouting is ikeward routing.
func newRouting(sys system) *cobra.Command {
	return &cobra.Command{
		Use:   "routing",
		Short: "Make the rules, the floors and the NAT rule, as at boot",
		Long:  routingLong,
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := sys.asRoot("routing", "changes the rules, routes and nftables"); err != nil {
				return err
			}
			return routing.System{Kernel: sys.network, NAT: sys.nat, Files: sys.files,
				Out: command.OutOrStdout()}.Run()
		},
	}
}
