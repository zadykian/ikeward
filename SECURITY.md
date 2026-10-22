# Security

ikeward runs as root, writes a VPN client's key, and decides where a server's traffic goes. A flaw
in it may leak that key, or put the remote network's traffic on the uplink in the clear.

## Supported versions

A fix goes into the next release, and no older release gets one. `install.sh` installs the latest
release by default, and updates to it when run again.

## Reporting a vulnerability

Report it privately, through GitHub's private vulnerability reporting: on the repository's
Security tab, Report a vulnerability. Never in an issue, a pull request or a discussion, which
anyone can read.

Say what goes wrong, on which release and system, and how to make it happen. Leave out every
profile, key, certificate and password. Leave out any employer's names, hosts, addresses and
domains too: make values up, as the tests do, from documentation ranges and `example.test`.

The maintainer answers in the report, fixes the flaw in a release, then publishes the advisory.

## What counts

- A secret that ikeward prints, logs, or writes where a user other than root may read it.
- Traffic for the remote network that leaves by the uplink in the clear while `status` passes.
- A way for a user other than root, the VPN server or the network to run a command or change a
  file through ikeward.
- An `install.sh` that installs a binary other than the release's, or one its checksum doesn't
  match.

A release forged on GitHub itself is beyond what ikeward checks: `install.sh` checks the binary
against a checksum from the same release, as the [README](README.md#security) says.
