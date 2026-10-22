# Status and the watchdog

`ikeward status` runs the watchdog's checks and changes nothing. The [README](../README.md) says how
to install ikeward and run its commands.

## The checks

`status` prints a line per check, `ok` or `FAIL`, with the check's name and what it found:

- `IKE SA`: strongSwan's IKE SA is up, through its VICI socket;
- `CHILD SA`: that SA's CHILD SA is up, through the same socket;
- `address`: the address the server assigned is on `ikeward0`, the tunnel's link, through netlink;
- `rule`: ikeward's rules look up the main table, then table 220, and no other rule looks up table
  220, through netlink, as [the order](nat.md#the-order) says;
- `route`: table 220 routes each range the CHILD SA names on the server's side through `ikeward0`,
  as `updown` sets them, through netlink. Those are the ranges the VPN server granted. The line
  names those `updown` refuses, as [the routes](nat.md#the-routes) says;
- `resolver`: the remote network's resolver, `install`'s `--dns`, answers a DNS query for
  `--probe`'s name with an address, and is asked only where the checks above pass. The query leaves
  by `ikeward0` alone, its socket bound there, so it goes through the tunnel or nowhere, never by
  the uplink. A reply to any of the query's sends counts, so a lost datagram does not make the
  watchdog restart a working tunnel;
- `DNS`: systemd-resolved sends the names under each `--domain` on `ikeward0` to that resolver, and
  only those, as `resolvectl` says, and `ikeward0` holds its own address, through netlink;
- `floor`: table 220 holds the floors, whether the tunnel is up or not, through netlink, as
  [the floors](nat.md#the-floors) says. That's `unreachable RANGE metric 4000` for each `--floor`,
  and the same for each range `/var/lib/ikeward/learned-ranges` lists. The line ends with those
  ranges, or `none yet` before the tunnel first came up, and those it passes over;
- `NAT`: the table `ip ikeward` holds its chain and rule, through netlink. The line says how many
  connections took the rule since the table was made. Where the table is missing or not ikeward's,
  the line says what a forwarded network loses. Where nftables won't list it to a user other than
  root, the line gives the error alone.

The `resolver`, `DNS` and `floor` checks read `install`'s values from
`/var/lib/ikeward/settings`, which `install` writes. Without the file, each fails, and says why.

It exits 0 when every check passes, and 1 otherwise.

## The remote network's names while the tunnel is down

resolved sends lookups on a link only while it has a routable address. So `ikeward0` holds one of
its own, `fd22:851f:35dd::1/128`, beside the one strongSwan puts there. While the tunnel is down,
at boot, in an outage or during a restart, lookups of the names under each `--domain` still go out
`ikeward0` to the remote network's resolver alone. strongSwan ignores the address, and doesn't tell
the VPN server of it. Without the address, the lookups would go to the uplink's resolver in the
clear. A public zone may answer some of the remote network's names, with their private addresses.
So they would resolve, and name each service to the uplink's resolver. Each `--domain` covers its
own name too, as a public site's, which the server can't look up either while the tunnel is down.

No SA takes such a query, so `ikeward0` drops it, and the kernel answers resolved's send with an
ICMP error. resolved then fails the lookup at once, with "No route to host", and asks no more. Not
one query leaves by another link. `ip -s link show ikeward0` counts them among the link's dropped
packets. Once the tunnel is back, lookups go through it at once.

The `DNS` check fails without the address, whether the tunnel is up or down, and passes with it.
networkd puts it back:

```sh
networkctl reconfigure ikeward0
```

As networkd sets `ikeward0` up, it enables IPv6 there alone, for that address and the link-local
one, even where `net.ipv6.conf.all.disable_ipv6=1` turns it off elsewhere. Written while the
server runs, as by `sysctl --system`, that setting takes every IPv6 address off every link, this
one among them, until the command above.

Where the kernel's command line turns IPv6 off, `ipv6.disable=1`, networkd ignores the address and
logs a warning. The command above doesn't help there, and the `DNS` check keeps failing. Drop
`ipv6.disable=1`, and set `net.ipv6.conf.all.disable_ipv6=1` instead.

## The server's networks inside the remote network's

A network of the server's may lie inside a range of the remote network's, as a LAN at
`10.88.88.0/24` inside `10.88.0.0/16`. So may a Docker network, from the private ranges Docker
takes its networks from. Their traffic stays on the server's side, the tunnel up or down, as
[the order](nat.md#the-order) puts the main table's routes first. SSH from a laptop on the LAN
reaches the server, and the server's replies go back over the LAN.

A host of the remote network's inside such a network is out of reach, as from that laptop. A lookup
of the remote network's names still goes through the tunnel over UDP, as resolved sends it out
`ikeward0`, whatever main routes.

A host inside the remote network's range that the server reaches through a router, by its default
route, is another matter. That's SSH or a WireGuard peer from another of the site's subnets, as a
Wi-Fi at `10.88.1.0/24` behind the LAN's router. The main table routes it by the default route
alone, which rule 32000 passes over. So the server's replies take table 220's route into the tunnel.
`ikeward0` drops them there, as no SA takes them, and a floor while the tunnel is down, or outside
the ranges granted. Give main a route to that network, through the router, and it comes first too:

```sh
ip route add 10.88.1.0/24 via 10.88.88.1
```

A route that networkd sets, in the LAN's `.network`, outlives a reboot. No check names such a host.

## The watchdog

`watchdog` runs the same checks every 5 minutes from 3 minutes after boot, as
`ikeward-watchdog.timer` starts it. It prints nothing while they pass. Where one fails, it prints
those that fail and restarts strongSwan, at most once in 10 minutes, as
`/var/lib/ikeward/last-restart` records. It then checks again for up to 30 seconds.

Where the `rule`, `floor` or `NAT` check fails, the watchdog first makes that part again, as
`ikeward routing` does, and logs it: a floor for each range the record lists. It then checks again:
a restart of strongSwan makes none of them, and would take a working tunnel down. Where all pass
then, it's done. Where the kernel refuses a part, the watchdog goes on with the other checks, then
exits 3. A restart they need brings the tunnel up again.

The watchdog leaves strongSwan alone where only these fail, which a restart doesn't change:

- the `DNS` check, `ikeward0`'s own address and resolved's settings, which networkd sets;
- the `rule`, `floor` and `NAT` checks, ikeward's routing, which the watchdog makes itself;
- the `resolver` check where the resolver answers with an error code or no address: the answer
  came through the tunnel, so the tunnel works;
- any check that can't read `/var/lib/ikeward/settings`, which `install` writes.

Once only the failures listed above repeat twice in a row after a restart, it stops checking.
Where the resolver doesn't answer at all, the watchdog restarts strongSwan, as the tunnel may carry
nothing.

strongSwan connects again by itself where it can. Where the VPN server stops answering,
strongSwan drops the SA after about 3 minutes and connects anew: 30 seconds until DPD's check, then
165 seconds of retransmits. It starts another attempt each time the server leaves one unanswered,
until the tunnel connects or an error ends it for good, as a refused login. Where the server's
name does not resolve, it tries again every 30 seconds.

A DELETE from the VPN server leaves the tunnel down until the watchdog's next restart. The
server sends one as it shuts down. Where it keeps one SA per identity, it sends one too as a phone
or a laptop logs in with the profile's identity. Were strongSwan to connect again at once, it and
that device would then take the SA from each other in a loop.

The watchdog exits 1 while a check fails, which `ikeward-watchdog.service` counts as success. It
exits 3 where it fails itself, which the service counts as a failure. That's where it cannot read
or write its record, `systemctl` fails, or the kernel refuses a part of the routing. So are the
settings or a record of the learned ranges that it cannot read. Its lines are in the journal as
warnings, under its own tag:

```sh
journalctl -t ikeward-watchdog
```
