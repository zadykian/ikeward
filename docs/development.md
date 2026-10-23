# Building and testing

Building and checking need Go, at the version `go.mod` names, and curl. The checks need git too,
in a work tree it can read: a clone, not an archive of one. The [README](../README.md) says how to
install ikeward and run its commands.

```sh
make check                    # shellcheck, shfmt, gofmt, go vet, golangci-lint, denycheck, tests
make lint                     # every gate, as CI's lint job runs them (checks.md)
make lint-local               # all but govulncheck, zizmor offline, as the lint plugin runs it
make vale FILES=README.md     # one gate, on the files named
make test                     # go test -count=1 ./...
go test -count=1 -run 'TestVersion$' ./cmd/ikeward   # a single test
go test -count=1 ./tests/installer                  # install.sh, in sh and bash, with a fake curl
go test -count=1 ./tests/tools                      # the gates' scripts, in scratch repositories
make build VERSION=X.Y.Z      # in dist: ikeward-linux-amd64 and -arm64, static, stamped X.Y.Z,
                              #   ikeward.sha256 and install.sh, as a release publishes them
make notices                  # dist/third-party-notices.txt, the licenses of what a build holds
make history                  # gitleaks, profilecheck and denycheck over every ref's history
make integration              # the VPN end to end in Docker Compose, as CI runs it
```

`make check`, `make lint` and `make test` fetch the tools at the versions `tools/run` pins into
`.cache/tools`, once. `make test` fetches Vale, gitleaks and shfmt, for the tests of the gates.
[The checks](checks.md) says what each gate of `make lint` checks.

## The installer's tests

`tests/installer` pipes `install.sh` into `sh` and `bash`. It runs each block of the README's
install commands on its own too, as an operator pastes it, in `sh` and `bash`. A block that sets
`pipefail` runs in `sh` only where `sh` has it, which Ubuntu 24.04's `dash` lacks. A fake `curl`
serves test releases over HTTPS alone and writes only into the test's directory, so no test reaches
the network. A fake `less` keeps what it shows. A fake `uname` and `id` say the machine and the
user, and the `PATH` holds nothing else but the tools the script runs. The script installs into a
directory of the test's, through `IKEWARD_INSTALL_DIR`, never into `/usr/local/sbin`.

## The integration test

`make integration` runs `tests/integration` in Docker Compose. A strongSwan server, a host on the
remote network behind it with a DNS server, and an Ubuntu 26.04 client run there. The server
narrows the client's `0.0.0.0/0` to two ranges: `192.0.2.0/24`, the remote network, and
`198.18.0.0/24`, where the host holds an address too. The client runs systemd and Docker. A CA made
for the test issues the server's certificate and the client's profile. `ikeward install` runs on the
client with that profile, the host as its resolver and `example.test` as its domain. Its floors are
`10.88.0.0/16`, and `192.0.2.0/23` around the remote network. The test checks that:

- `install`, on a fresh client whose strongSwan runs as shipped, removes strongSwan's rule for table
  220, and another there that charon doesn't own. Then networkd makes `ikeward0`, an XFRM interface
  with the interface ID the SAs' policies take;
- the tunnel comes up, and every check of `status` passes;
- `install` run again plans nothing, and `--exit-code` exits 0. With `--all-steps`, it plans all
  seven steps and exits 4. With the watchdog's timer disabled by hand, it plans and runs the step
  that enables it alone, and exits 4;
- `updown` routes both ranges through `ikeward0`, makes their floors and records them. The routing's
  unit made the floors of `--floor` alone before the tunnel first came up;
- resolved sends a `*.example.test` lookup to the remote resolver, through the tunnel, and no
  other;
- `status` asks its resolver out `ikeward0` alone. With the far host's second address,
  `198.18.1.53`, in the settings, its `resolver` check fails, and the host logs no query. A
  lookup through a default route by the VPN server gets the host's answer there, in the clear;
- with strongSwan stopped, and a default route through the VPN server, resolved still sends a
  `*.example.test` lookup out `ikeward0` alone, which drops it, and the remote resolver sees
  none. The `DNS` check passes. Once strongSwan is back, that lookup's name resolves within 30
  seconds;
- without `ikeward0`'s own address, the `DNS` check fails with the tunnel up, and
  `networkctl reconfigure` puts the address back;
- Docker's containers reach the remote network from the address the server assigned, through
  the NAT rule;
- the watchdog restarts strongSwan once the address the server assigned is gone from `ikeward0`,
  which puts it back, and logs its lines as warnings;
- a second Docker network stands in for a forwarded one: Docker NATs none of its traffic, and the
  client NATs it to the uplink alone. It reaches the remote network through the NAT rule;
- without the NAT rule, Docker's containers still reach the remote network through Docker's
  MASQUERADE out `ikeward0`, and the forwarded network doesn't. The watchdog makes the rule again,
  with the floor and a rule of the order, and leaves the tunnel alone. Where it cannot read its
  record of the last restart, it leaves strongSwan alone, exits 3, and its service fails;
- the routes go as strongSwan stops, within 5 seconds, and come back as it starts. They go once
  the tunnel is down, and come back as it goes up again. strongSwan then tells the server of no
  IPv6 address, as it ignores `ikeward0`'s own. The floors stay throughout;
- with the VPN server's own range granted too, `updown` refuses it and learns the others, and the
  IKE SA still rekeys. Written into the record by hand, the routing's unit passes over it. As a
  `--floor`, `install` refuses it, as the client's `/etc/hosts` resolves the server's name;
- with every address granted, `updown` refuses it: no route, floor or record. The IKE SA still
  rekeys, the client's own traffic keeps its default route, and the LAN host stays in reach. The
  remote network's ranges are unreachable, but its names resolve through the tunnel;
- the client has a LAN inside `10.88.0.0/16`, with a host on it. With the tunnel up, the client
  and a container reach that host directly, and it reaches the client. The client, a container and
  the forwarded network reach the remote host in both ranges through the tunnel, past the wider
  floor around the remote network. The floor `10.88.0.0/16` makes an address inside it that the
  server grants none of unreachable;
- with strongSwan stopped, and a default route through the VPN server, those connections to the
  remote network fail at once, with "No route to host". No packet for it leaves the client, and the
  LAN stays in reach. Without the floors, the packets leave by the uplink in the clear, and the
  remote host answers them, until the routing's unit makes the floors again;
- after a restart of the client, as a reboot, the routing's unit made the rules, the floors and
  the NAT rule before networkd and strongSwan started. systemd finds no ordering cycle, and orders
  it after `nftables.service`;
- `uninstall` leaves no NAT rule, none of ikeward's rules, no route in table 220 and no `ikeward0`.
  It stops and disables strongSwan, and removes the units of the watchdog and the routing, every
  file `install` wrote and the record of the learned ranges.

It needs Docker with Compose, and these kernel modules loaded, as no container can load them. They
are those of the tunnel, its link `ikeward0` and the NAT rules:

```sh
modprobe -a esp4 xfrm_user xfrm_interface nft_chain_nat nft_nat nft_masq
```

The client runs privileged. Its image masks the units that would reach the host's kernel or
devices. Before systemd starts, `/sys`, and `/proc/sys` but for `/proc/sys/net`, turn read-only,
and `/dev/kmsg` becomes `/dev/null`. The test's networks are internal and give the host no address
on them. So the remote network and the LAN reach neither the host's routes nor the Internet.

As it ends, the test removes its containers, networks, volumes and the images it built. It keeps
`ubuntu:26.04` and Docker's build cache, for the next run. A run that was killed leaves its
services up. The next run removes them first, or remove them by hand:

```sh
docker compose -p ikeward-integration down --volumes --remove-orphans --rmi local
```

## What ikeward reads of a profile

To see what ikeward reads of a client profile, name its files in `IKEWARD_PROFILE` by their path
without the extension, as `/root/vpnclient` for `/root/vpnclient.p12`. The test reads the `.p12`
and the `.sswan`, and the `.mobileconfig` where there is one. It logs the server, the proposals,
the certificate, the CA and the file it came from. It logs whether the `.p12` has a password, but
no secret. It reads a `.p12`'s password from the file that `IKEWARD_PROFILE_PASSWORD_FILE` names.

```sh
IKEWARD_PROFILE=PATH/NAME go test -count=1 -v -run 'TestIssuedProfile$' ./internal/profile
```
