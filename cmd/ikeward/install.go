package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/profile"
	"github.com/zadykian/ikeward/internal/routing"
)

// installLong is a variable, as it shows routing.MinBits.
var installLong = `Install the VPN from the client profile's files, with the remote network's
values, as root:

  - install strongSwan's packages with apt-get, where they are missing, and
    where that fails, update the package lists and try once more;
  - write the files that differ from what the profile gives, naming each: the
    swanctl connection, the certificate, the CA, the key, which only root may
    read, strongSwan's drop-in, networkd's link ` + files.Link + `, the tunnel's, and its
    settings, the watchdog's units, the routing's, ` + routing.Unit + `,
    and ` + files.SettingsFile + `, which holds the remote network's values.
    ` + install.Record + ` lists them for uninstall, and install removes
    those it listed but no longer writes;
  - take them up in steps: reload systemd and networkd, and enable the
    routing's unit, strongSwan and the watchdog's timer. Restart the routing's
    unit, which makes what is missing of ikeward's rules and floors, and the
    NAT table anew, before strongSwan starts, as routing --help says. Then
    load the connection, and start the watchdog's timer. networkd or
    strongSwan restarts where its own settings changed, as it reads them only
    as it starts.

A step runs only where it is due, and install says why. It is due where:

  - install changes a file the step reads;
  - an earlier install that stopped half way owes it, as
    ` + install.Journal + ` names the steps due from before the files
    change until install ends;
  - or a probe finds that the system needs it: a unit disabled, stopped or
    changed on disk, the link ` + files.Link + ` or its own address missing, a failing
    rule, floor, NAT or DNS check, as status --help says, or strongSwan
    lacking the connection or its certificates.

The profile's files are those ikev2setup.sh writes, named alike, each given
by its path:

  - --p12 FILE: NAME.p12, the client's key and certificate. NAME names the
    files install writes;
  - --sswan FILE: NAME.sswan, which names the VPN server and the proposals;
  - --mobileconfig FILE: NAME.mobileconfig, read only where neither the .p12
    nor the .sswan holds the CA. Leave it out otherwise.

The remote network's values come from the flags, and routing, status and the
watchdog read them from ` + files.SettingsFile + `:

  - --dns ADDR: the remote network's resolver, an IPv4 address. ` + files.Link + `'s
    network sends it the remote network's names, and no other lookup;
  - --domain NAME: a domain of the remote network's, whose names go to ADDR
    alone, as routing domains. Give it once for each domain;
  - --probe NAME: a name ADDR answers, which status asks for through the
    tunnel. It is the first --domain where not given;
  - --floor CIDR: a range that keeps a floor from boot on, whatever updown
    learns, as routing --help says. Give it once for each range, or not at
    all: updown learns the remote network's ranges from the tunnel. install
    refuses one wider than /` + minBits + `, or one that holds the VPN server's address,
    as the profile gives it or its name resolves now. Such a floor would keep
    the tunnel from coming up. routing passes over a floor that holds an
    address updown records later.

The connection asks the VPN server for every address, which it narrows to the
remote network's ranges, and updown learns them, as updown --help says. A
network of the server's inside one, as a LAN or a Docker network, keeps its
traffic: the server's own networks come first, as routing --help says.

The watchdog's service, the routing's and strongSwan run
` + files.Binary + `, where install.sh puts ikeward, and install refuses
to run as any other program.

Where the profile's .p12 has a password, install reads it from standard input
when that is not a terminal, and asks for it otherwise.

A network the server forwards to the remote network needs nothing more: table
ip ` + routing.NATTable + ` NATs whatever leaves by ` + files.Link + `. ikeward sets no
forwarding: the server's firewall decides which networks reach the remote
network, as ufw route allow in on wg0 out on ` + files.Link + ` does.

--dry-run prints what install would change and run, the files as unified diffs
but for those only root may read, and changes nothing.

--all-steps takes every step, due or not, as earlier versions did. It is for a
change no probe sees, such as a connection loaded by hand under the same name.`

// changesSettings is what install and uninstall do that calls for root.
const changesSettings = "changes the system's settings"

// installFlags are install's flags: the profile's files, the remote network's values, and how
// install runs.
type installFlags struct {
	named            profileFlags
	given            flags
	dryRun, allSteps bool
}

// newInstall is ikeward install.
func newInstall(sys system) *cobra.Command {
	var f installFlags
	command := &cobra.Command{
		Use: "install --p12 FILE --sswan FILE [--mobileconfig FILE] --dns ADDR\n" +
			"      --domain NAME... [--probe NAME] [--floor CIDR]... [--dry-run]\n" +
			"      [--all-steps]",
		Short:                 "Install the VPN from the client profile",
		Long:                  installLong,
		Args:                  noArgs,
		DisableFlagsInUseLine: true,
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := sys.install(command, f)
			return err
		},
	}
	command.Flags().StringVar(&f.named.p12, "p12", "", "the client profile's .p12 `FILE`")
	command.Flags().StringVar(&f.named.sswan, "sswan", "", "the client profile's .sswan `FILE`")
	command.Flags().StringVar(&f.named.mobileconfig, "mobileconfig", "",
		"the client profile's .mobileconfig `FILE`, for its CA")
	command.Flags().StringVar(&f.given.dns, "dns", "",
		"the remote network's resolver, an IPv4 `ADDR`")
	command.Flags().StringArrayVar(&f.given.domains, "domain", nil,
		"a domain `NAME` of the remote network's, once for each")
	command.Flags().StringVar(&f.given.probe, "probe", "",
		"a `NAME` the resolver answers (default the first --domain)")
	command.Flags().StringArrayVar(&f.given.floors, "floor", nil,
		"a `CIDR` kept off the uplink from boot on, once for each")
	command.Flags().BoolVar(&f.dryRun, "dry-run", false, "print the changes, and change nothing")
	command.Flags().BoolVar(&f.allSteps, "all-steps", false, "take every step, due or not")
	return command
}

// install installs the VPN as f asks, on command's output. It returns whether it changed the
// system, or in a dry run would.
func (s system) install(command *cobra.Command, f installFlags) (bool, error) {
	if f.named.p12 == "" || f.named.sswan == "" {
		return false, usageError{errors.New("install needs --p12 FILE and --sswan FILE"), command}
	}
	settings, err := f.given.settings()
	if err != nil {
		return false, usageError{err, command}
	}
	if err := s.asRoot("install", changesSettings); err != nil {
		return false, err
	}
	if err := s.asBinary(command.OutOrStdout(), f.dryRun); err != nil {
		return false, err
	}
	read, err := f.named.read()
	if err != nil {
		return false, err
	}
	p, err := profile.Parse(read, s.password)
	if err != nil {
		return false, err
	}
	if err := s.clearOfServer(command, p.Server, settings.Floors); err != nil {
		return false, err
	}
	in := s.installer(command, f.dryRun)
	in.AllSteps = f.allSteps
	return in.Install(command.Context(), p, settings)
}

// installer is install's and uninstall's view of sys, saying what they do on command's output.
func (s system) installer(command *cobra.Command, dryRun bool) install.System {
	return install.System{Files: s.files, Runner: s.runner, Tables: s.nat, Network: s.network,
		Charon: s.charon, Out: command.OutOrStdout(), DryRun: dryRun}
}
