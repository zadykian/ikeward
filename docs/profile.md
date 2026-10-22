# The profile

`ikeward install` reads the client's profile from its files, each given by its path. The
[README](../README.md) says how to install ikeward and run its commands.

## The files

hwdsl2's `ikev2setup.sh` writes a client's profile as three files, each named for the client,
`vpnclient` by default. ikeward takes two of them as they are, and reads no archive, such as a zip:

- `NAME.p12`, given by `--p12`, holds the client's key and certificate, and the CA that issued
  them, which `ikev2setup.sh` always puts there.
- `NAME.sswan`, given by `--sswan`, is the profile for strongSwan's app for Android, in JSON. It
  names the VPN server and the proposals, and holds the `.p12` again.
- `NAME.mobileconfig`, given by `--mobileconfig`, is the profile for Apple's systems. It holds
  another copy of the `.p12`, and, where the files have no password, that copy's password in plain
  text. ikeward reads it only where neither the `.p12` nor the `.sswan` holds the CA. That never
  happens with `ikev2setup.sh`'s files, so leave `--mobileconfig` out.

The files share their NAME, which names the files `install` writes from them. Each of the three
files holds the client's key. Keep them where only root may read them, and never put them in an
issue or a repository.

## What ikeward reads

Of the `.sswan`, ikeward reads these fields, and passes over the rest:

- `type`, which must be `ikev2-cert`, a login by certificate;
- `remote.addr`, the VPN server's host name or address, and `remote.port`, 500 where missing;
- `remote.id`, the server's identity, its address where missing, and `local.id`, the client's, the
  certificate's subject where missing;
- `ike-proposal` and `esp-proposal`, in strongSwan's syntax, as the connection takes them;
- `remote.cert`, a CA in Base64, which strongSwan's app checks the server by.

Of the `.p12`, it reads the key, the certificate that matches it, and the other certificates. Where
the `.p12` has a password, `install` reads it from standard input when that isn't a terminal, and
asks for it otherwise. `ikev2setup.sh` sets one where asked to protect the files, and prints it.

The CA is the certificate that issued the client's. ikeward takes it from the first that holds it:
the `.p12`, the `.sswan`'s `remote.cert`, then the `.mobileconfig`'s root payloads. The connection
accepts the server's certificate from that CA alone.

`install` prints what it read: the server, its port and identity, the proposals, the certificate
and the CA, each with its end of validity. It prints no key, and no password. `install` writes the
client's certificate as `/etc/swanctl/x509/NAME-cert.pem`, and the CA as
`/etc/swanctl/x509ca/ikeward-ca.pem`. The key goes in `/etc/swanctl/private/NAME-key.pem`, which
only root may read.

## What ikeward refuses

`install` names the file and what is wrong with it, and changes nothing. Where it cannot read a
file, it names the flag that gave it. It refuses the files where:

- they are not named alike, as two profiles' would be;
- one of them is over 1 MiB, more than a profile's file holds;
- the `.p12`'s name holds a sign other than letters, digits, `.`, `_` and `-`, or starts with one
  of the last three.

It refuses a `.sswan` that:

- is not JSON, or is of a type other than `ikev2-cert`, as one with a login by password;
- names a server that is neither a host name nor an address, or a port that is none;
- holds an identity with a quote, a backslash or a sign that doesn't print;
- holds proposals outside strongSwan's syntax;
- pins the server's own certificate in `remote.cert`, rather than its CA. ikeward checks the server
  by a CA alone, and won't drop the pin;
- names a CA in `remote.cert` other than the one that issued the client's certificate.

It refuses a `.p12` whose password is missing or wrong, whose key can't sign, or that holds no
certificate of its key. Where no file holds the CA, it says where it looked. It refuses a signed
`.mobileconfig` too, and reads an unsigned XML one alone.

To see what ikeward reads of a profile without installing it, run the test that
[building and testing](development.md#what-ikeward-reads-of-a-profile) names.
