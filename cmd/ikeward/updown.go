package main

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/updown"
)

// minBits is the shortest prefix of a range updown routes, as updownLong shows it.
var minBits = strconv.Itoa(routing.MinBits)

// updownLong is a variable, as it shows the floors' metric and the shortest prefix from routing.
var updownLong = `Route each range the VPN server grants through the tunnel, as root.
strongSwan runs ` + files.Binary + ` updown as the tunnel goes up or down, as
the connection's updown says, once for each range the server granted. It says
which in its environment: the verb in PLUTO_VERB, the range in
PLUTO_PEER_CLIENT.

As the tunnel goes up, PLUTO_VERB=` + updown.Up + `, updown routes the range through
` + files.Link + `, the tunnel's link, in table 220, which ikeward's rule looks up after
the server's own networks, as routing --help says. charon routes nothing
there, as the connection's SAs take ` + files.Link + `'s interface ID. The kernel
removes the routes as the address the server assigned leaves ` + files.Link + `, so
updown sets them each time. It makes the range's floor too, unreachable at
metric ` + floorMetric + `, below the route, and records the range in
` + routing.Learned + `, for the floors routing makes as the
server boots, with the VPN server's address, PLUTO_PEER.

updown refuses a range wider than /` + minBits + `, as 0.0.0.0/0 or 0.0.0.0/1, which would
take the server's own traffic into the tunnel. It refuses one that holds the
VPN server's address too, which would take IKE's and ESP's own packets there,
and whose floor would keep the tunnel from coming up after a reboot. It
routes, floors and records neither, says so, and exits 0, so that strongSwan
keeps the SA up for the other ranges. routing passes over such a range in the
record too.

As the tunnel goes down, PLUTO_VERB=` + updown.Down + `, updown removes the range's
route, unless strongSwan has another SA of the connection up that grants it,
as in a reauthentication, which brings up the new SA before it deletes the
old one: then it sets the route again. It asks strongSwan through VICI, but
not as systemd stops or restarts ` + files.StrongSwan + `: charon then no longer
answers, and no SA stays up, so updown removes the route at once. Where
strongSwan does not answer within 3 seconds otherwise, updown leaves the
route, which takes no traffic while the tunnel is down, and exits 1.

The floors stay: the remote network's traffic goes nowhere while the tunnel is
down.
updown passes over any other verb, as an IPv6 SA's.

To see the routes, the floors below them, and the ranges learned:

  ip route show table 220
  cat ` + routing.Learned

// newUpdown is ikeward updown.
func newUpdown(sys system) *cobra.Command {
	return &cobra.Command{
		Use:                   "updown",
		Short:                 "Set or remove a route through the tunnel, as strongSwan runs it",
		Long:                  updownLong,
		Args:                  noArgs,
		DisableFlagsInUseLine: true,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := sys.asRoot("updown", "changes the routes"); err != nil {
				return err
			}
			return updown.System{Route: sys.route, Files: sys.files, Tunnel: sys.updownTunnel,
				Runner: sys.runner, Env: sys.env, Out: command.OutOrStdout()}.Run(command.Context())
		},
	}
}
