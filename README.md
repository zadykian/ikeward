# ikeward

ikeward keeps a Linux server on an IKEv2 VPN, around the clock. From the client profile that
hwdsl2's [ikev2setup.sh][ikev2] makes, it sets up strongSwan, the remote network's DNS, the routing
and a watchdog. The server, its containers and each network it forwards then reach the remote
network through the tunnel. The server's own networks come first, even inside the remote network's
ranges. While the tunnel is down, nothing for the remote network leaves by the uplink.

It suits a server that needs a private network behind a VPN, as an office's or a lab's, for itself,
its containers or its WireGuard peers. The VPN server logs clients in by certificate, as the
Libreswan server that `ikev2setup.sh` sets up does, and grants the remote network's ranges alone,
by [split tunneling][st]. The integration test runs a strongSwan server in its place.

## What it needs

- Linux on amd64 or arm64 with `apt-get`, systemd, and systemd-networkd and systemd-resolved
  running, as on Ubuntu Server. Debian needs the `systemd-resolved` package and networkd enabled.
  The integration test runs Ubuntu 26.04.
- Root, and `curl` and `sha256sum` for the installer.
- The profile's `.p12` and `.sswan`, as [the profile](docs/profile.md) says.
- A resolver inside the remote network's ranges, and the domains it answers for.

## Install

As root, in `bash` or another shell with `pipefail`, install the latest release:

```sh
set -o pipefail
curl -fsSL --proto '=https' --tlsv1.2 \
    https://github.com/zadykian/ikeward/releases/latest/download/install.sh | sh
```

Or a given release, its version in place of `X.Y.Z` twice:

```sh
set -o pipefail
curl -fsSL --proto '=https' --tlsv1.2 \
    https://github.com/zadykian/ikeward/releases/download/vX.Y.Z/install.sh \
    | IKEWARD_VERSION=X.Y.Z sh
```

Both blocks set `pipefail` themselves, and each installs one release. Without `pipefail`, a `curl`
that fails pipes nothing into `sh`, which exits 0 having installed nothing. A script that runs the
pipe must set it too. `curl` fetches over HTTPS alone, and `-f` has it fail on an error page rather
than pipe it into `sh`.

Or read the script before it runs as root. `sh` runs it as `less` quits, and only where `curl`
downloaded it, so that a failed download runs no older `install.sh`:

```sh
curl -fsSLO --proto '=https' --tlsv1.2 \
    https://github.com/zadykian/ikeward/releases/latest/download/install.sh &&
    less install.sh && sh install.sh
```

`install.sh` downloads the binary for the machine, `ikeward-linux-amd64` or `ikeward-linux-arm64`,
from the latest release, or from the one `IKEWARD_VERSION` names. It checks the binary against the
release's `ikeward.sha256` and runs it for its version, then moves it to `/usr/local/sbin/ikeward`.
Run again, it updates ikeward; then run `ikeward install` again, for what the new version changes.

## Set up

As root, give `install` the profile and the remote network's values:

```sh
ikeward install --p12 vpnclient.p12 --sswan vpnclient.sswan --dns 10.88.0.53 \
    --domain example.internal --floor 10.88.0.0/16
```

`--dns` is the remote network's resolver, which each `--domain`'s names go to alone, through the
tunnel. `--probe` is a name for `status` to ask it, the first domain by default. Each `--floor` is a
range kept off the uplink from the first boot on, before the tunnel ever came up. `install`
installs strongSwan's packages, writes the files that differ, then takes up what changed and mends
what it finds amiss. Run it again to change a value, `--dry-run` first; `--exit-code` exits 4 where
anything changes. Each command's `--help` says the rest. The other commands run as root too:

```sh
ikeward status                              # the watchdog's checks, a line each; changes nothing
ikeward watchdog                            # the checks; mends the routing, or restarts strongSwan
ikeward updown                              # run by strongSwan: sets or removes the route
ikeward routing                             # run at boot: the rules, the floors and the NAT rule
ikeward uninstall [--dry-run]               # removes what install wrote; the packages stay
```

`watchdog` runs every 5 minutes. It makes missing rules, floors or the NAT rule again itself, and
restarts strongSwan where a restart can mend a check that fails.

## The routing

`install` writes the tunnel's link, `ikeward0`, an XFRM interface that networkd makes. What leaves
by it goes through the tunnel's SAs, or nowhere. `ikeward-routing.service` runs `ikeward routing`
as the server boots, and gives every packet one order. The server's own networks come first, then
the remote network's, through `ikeward0`, then the default route. So a LAN at `10.88.88.0/24` keeps
its traffic, though a range of the remote network's holds it, as [the order](docs/nat.md#the-order)
says.

The connection asks the VPN server for every address, and the server narrows that to the remote
network's ranges. strongSwan runs `updown` for each, which routes it through `ikeward0`, makes its
floor and records it, but for one wider than `/8` or holding the VPN server's address. While the
tunnel is down, the floors, unreachable routes, take each range learned and each `--floor`, so a
connection there fails at once. One NAT rule, `oifname "ikeward0" counter masquerade`, gives what
leaves by `ikeward0` the address the VPN server assigned, whatever its source.

## Limits

- IPv4 alone: the connection asks for no IPv6 range, and `updown` passes over one.
- One VPN per server, whose client logs in by certificate, a `.sswan` of type `ikev2-cert`.
- A VPN server that grants every address, as `ikev2setup.sh` sets it up by default, carries
  nothing: `updown` refuses that grant.
- One CHILD SA, whose ranges ikeward learns. Libreswan makes a tunnel of each range of
  `leftsubnets=`, and ikeward may learn one of them alone. Give the ranges in one `leftsubnet=`,
  which takes several, comma-separated, from Libreswan 5 on.
- A host of the remote network inside a network of the server's is out of the server's reach.
- ikeward sets no forwarding and no firewall rule, as
  [networks the server forwards](docs/nat.md#networks-the-server-forwards) says.

## Security

`install.sh` checks the binary against a checksum from the same release, which catches a broken
download, not a forged release. The profile's files hold the client's key, so keep them where only
root may read them, as `install` keeps the key. ikeward prints and logs no key and no password.
Report a vulnerability privately, as [SECURITY.md](SECURITY.md) says, never in an issue.

## Stop and uninstall

To stop the VPN, stop the watchdog's timer and service with strongSwan, or the watchdog starts
strongSwan again:

```sh
systemctl stop ikeward-watchdog.timer ikeward-watchdog.service strongswan.service
```

The floors stay, and keep the remote network's traffic off the uplink until the tunnel is up again.
`ikeward uninstall` removes them, with the routing and every file `install` wrote. Its traffic then
takes the default route. ikeward itself stays, until `rm /usr/local/sbin/ikeward`.

## More

- [Status and the watchdog](docs/status.md): each check, the remote network's names while the
  tunnel is down, the server's networks inside it, strongSwan's retries and the watchdog's repairs.
- [The routing and the NAT rule](docs/nat.md): the order, `updown`'s routes and the ranges it
  learns, the floors, the NAT rule, forwarded networks and the firewall, and setting them again.
- [The profile](docs/profile.md): its files, what ikeward reads of them, and what it refuses.
- [Building and testing](docs/development.md): the commands, the installer's and integration tests.
- [The checks](docs/checks.md): what `make lint` checks, and how a finding is settled.
- [Maintaining](docs/maintaining.md): landing pull requests, releases, and a fork's pull request.
- [Contributing](CONTRIBUTING.md), [security](SECURITY.md) and [the MIT license](LICENSE).

[ikev2]: https://github.com/hwdsl2/setup-ipsec-vpn/blob/master/docs/ikev2-howto.md
[st]: https://github.com/hwdsl2/setup-ipsec-vpn/blob/master/docs/advanced-usage.md#split-tunneling
