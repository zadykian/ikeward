# The routing and the NAT rule

`ikeward-routing.service` runs `ikeward routing` as the server boots. strongSwan runs
`ikeward updown` as the tunnel goes up or down, as the connection's `updown` says. The
[README](../README.md) says how to install ikeward and run its commands.

## The order

Every packet follows the same rules: the server's own, a container's and a forwarded network's.
`ip rule` lists them so:

```text
0:      from all lookup local
32000:  from all lookup main suppress_prefixlength 0
32001:  from all lookup 220 suppress_prefixlength 0
32766:  from all lookup main
32767:  from all lookup default
```

Rule 32000 looks up the main table, the server's own networks, but passes over its default route. So
a network of the server's comes first, even inside a range of the remote network's: a LAN at
`10.88.88.0/24` inside `10.88.0.0/16`, or a Docker network. Rule 32001 looks up table 220, where
`updown` routes the remote network's ranges through the tunnel, and passes over a default route
there too. Main's default route comes last. Neither side's addresses then depend on the other's.

A host of the remote network's inside a network of the server's is out of the server's reach, as
from a laptop on that network.
[Status and the watchdog](status.md#the-servers-networks-inside-the-remote-networks) says what that
means for hosts the server reaches through a router.

strongSwan made a rule of its own at priority 220, before main's, which put the remote network
first. ikeward's drop-in turns it off with `routing_table = 0`, and `ikeward routing` removes one
left behind, as by a charon that died.

## The routes

The tunnel is a link of its own, `ikeward0`, an XFRM interface that networkd makes. The
connection's SAs take its interface ID, so what leaves by `ikeward0` goes through them, and
`ikeward0` drops what none takes. It sends nothing in the clear. strongSwan puts the address the
server assigned there, and routes nothing for SAs with an interface ID.

The connection asks for every address, `remote_ts = 0.0.0.0/0`, and the VPN server narrows that to
the remote network's ranges, as its split tunneling sets them. strongSwan runs `updown` once for
each range granted, which it names in `PLUTO_PEER_CLIENT`.

So as the tunnel goes up, `updown` routes each range through `ikeward0`, in table 220. What the
server sends to the remote network then leaves by `ikeward0`, from the address on it. The kernel
removes the routes as that address leaves `ikeward0`. `updown` sets each route as the tunnel comes
up, and removes it as the tunnel goes down. It makes the range's floor at once, and records the
range in `/var/lib/ikeward/learned-ranges`, for the floors at boot.

Where strongSwan has another SA of the connection up that grants the range, `updown` keeps its route
instead. A reauthentication brings up the new SA before it deletes the old one. `updown` asks
strongSwan through VICI, but not as systemd stops or restarts `strongswan.service`. charon then no
longer answers, and no SA stays up, so `updown` removes the route without asking. Where strongSwan
doesn't answer within 3 seconds otherwise, `updown` exits 1 and leaves the route, which carries
nothing while the tunnel is down.

`updown` refuses two kinds of grant. It routes, floors and records neither, says so in
strongSwan's log, and exits 0, so that the SA stays up for the other ranges:

- a range wider than `/8`, the widest private block, as `0.0.0.0/0`, or `0.0.0.0/1` with
  `128.0.0.0/1`. It would take the server's own traffic to the Internet into the tunnel, and its
  floor would cut that traffic off while the tunnel is down. Rule 32001 passes over `0.0.0.0/0`
  alone;
- a range that holds the VPN server's address, as a range of the server's public addresses would.
  Its route would take IKE's and ESP's own packets into the tunnel. Its floor would keep the tunnel
  from coming up after a reboot, until someone edits the record.

`updown` records the VPN server's address too, on a line `server ADDRESS`. `ikeward routing` passes
over such a range in the record, as one written there by hand, and says so in the journal. The
`route` and `floor` lines of `status` name what was refused or passed over.

Where the server grants every address, the tunnel carries no connection. The floors keep the ranges
learned before unreachable, and other addresses take the default route. Only the remote network's
names go through, as resolved sends them out `ikeward0`, and the SA's policy takes them. The `route`
check of `status` fails, and the watchdog restarts strongSwan every 10 minutes.

## The floors

While the tunnel is down, no route through `ikeward0` exists. A packet to the remote network would
then pass over table 220 and take main's default route, out by the uplink in the clear. A floor
takes it first, an unreachable route in table 220 at metric 4000. There is one for each `--floor`
that `install` was given, and one for each range `updown` learned. A `--floor 10.88.0.0/16`, with a
grant of that range alone, makes one:

```text
unreachable 10.88.0.0/16 metric 4000
```

A connection the server opens to the remote network then fails at once, with "No route to host",
before any packet leaves. A container's or a forwarded network's packet goes no further than
routing, and its sender gets an ICMP error that says the same. Nothing for the remote network leaves
by the uplink, or by any other link.

A floor of a `--floor` stays whatever `updown` learns. On a first boot, before the tunnel ever came
up, it keeps the remote network's traffic off the uplink. Later, it covers what the record lacks: a
range the server grants less of, or a record that was lost. A `--floor` may cover the whole of the
remote network's private range, as known before ikeward learned its ranges. A public zone may name
hosts inside it, as
[Status and the watchdog](status.md#the-remote-networks-names-while-the-tunnel-is-down) says.
Traffic to one outside the grants goes nowhere, rather than to the uplink. A floor blocks no network
of the server's, as rule 32000 comes first.

`install` refuses a `--floor` that holds the VPN server's address as `install` finds it: the
profile's, or what the system's resolver gives for its name. Such a floor would keep charon's own
packets from the server, and the tunnel would never come up to record the address. `routing`
passes over a `--floor` that holds an address `updown` recorded since, as it does a range learned.

A floor stays once the server no longer grants its range: the record keeps every range learned,
until `uninstall` removes it. A `--floor` that `install` is no longer given keeps its floor until
the next boot. To drop one now, remove its line from `/var/lib/ikeward/learned-ranges`, where it's
there, then its floor:

```sh
ip route del unreachable RANGE table 220 metric 4000
```

`updown`'s route, at metric 0, comes before its range's floor while the tunnel is up. A floor names
no link, so the kernel keeps it as the address on `ikeward0` comes and goes. The remote network's
names are another matter: resolved still sends them out `ikeward0`, which drops them, as
[Status and the watchdog](status.md#the-remote-networks-names-while-the-tunnel-is-down) says.

## The NAT rule

`ikeward routing` makes an nftables table of its own, `ip ikeward`, as `nft list` shows it:

```text
table ip ikeward {
	chain postrouting {
		type nat hook postrouting priority srcnat - 1; policy accept;
		oifname "ikeward0" counter packets 0 bytes 0 masquerade
	}
}
```

The rule gives what leaves by `ikeward0` the address on it, the one the server assigned, which the
tunnel's SAs take alone. That holds whatever the source: the server's own, a container's or a
forwarded network's. The chain's priority is 99, before Docker's NAT rules at 100. The first NAT
rule that matches a connection decides, so no rule Docker adds can push it down. The counter counts
the connections that took the rule, as NAT sees their first packets alone.

Docker's own MASQUERADE takes the address of the link a packet leaves by, which is `ikeward0`'s for
the remote network. So Docker's containers reach the remote network even without the table.
A network that Docker doesn't NAT, as WireGuard's, reaches it through the table alone.

Masquerade drops its connections' conntrack entries as the address leaves `ikeward0`. A connection
opened while the tunnel is down fails in routing, before conntrack confirms an entry. So no
connection keeps a stale address once the tunnel is back.

## Networks the server forwards

A network the server forwards to the remote network, as WireGuard's `10.89.0.0/24`, needs nothing
of ikeward's. The NAT rule takes what leaves by `ikeward0`, and the connection's `updown` names no
network.

ikeward sets no forwarding: `net.ipv4.ip_forward` must be on. The server's firewall decides which
networks reach the remote network, as `ufw route allow in on wg0 out on ikeward0` does. Whatever it
forwards there leaves from the address the server assigned, from any interface and with any source.
So match a network's own interface in each rule that forwards out `ikeward0`. ufw's default
rules forward ICMP echo requests from any interface.

## Setting them again

`ikeward-routing.service` makes the rules, the floors and the table as the server boots, a floor for
each range of `/var/lib/ikeward/settings` and of the record. It runs before `network-pre.target`,
which networkd waits for, and before strongSwan. No default route exists yet, so none can take the
remote network's traffic first. The unit has no default dependencies, as Debian's `nftables.service`
has none. They would order it after `sysinit.target`, which networkd may start before. ikeward
leaves `nftables.service` alone: its `/etc/nftables.conf` starts with `flush ruleset`, which would
remove Docker's and ufw's rules. Where it's enabled, the routing's unit runs after it, so that its
flush comes before the table.

networkd leaves the rules and the routes in table 220 alone, as `ManageForeignRoutes=no` and
`ManageForeignRoutingPolicyRules=no` tell it. `install` restarts the unit before strongSwan starts,
where the unit or the settings change, the unit isn't active, or the `rule`, `floor` or `NAT` check
fails.

Something may still remove them while the server runs, as `nft flush ruleset` removes the table.
`status`'s `rule`, `floor` and `NAT` checks find each, and the watchdog makes it again within about
5 minutes, as [the watchdog](status.md#the-watchdog) says. Meanwhile, without the table, a forwarded
network's connections go nowhere. Without rule 32000, the remote network comes before the
server's own. Without rule 32001 or a floor, the remote network's traffic takes the default route
while the tunnel is down. To make them at once:

```sh
systemctl restart ikeward-routing.service
```

`uninstall` stops strongSwan first, then removes the table, the rules and table 220's routes, the
floors among them, and the record. Traffic to the remote network then takes the default route, as on
a server without ikeward. strongSwan logs what `updown` prints, and the journal what
`ikeward routing` prints. `ip`, `nft` and the record list the rest:

```sh
journalctl -u strongswan.service | grep updown
journalctl -u ikeward-routing.service
ip rule
ip route show table 220
nft list table ip ikeward
cat /var/lib/ikeward/learned-ranges
```
