package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/profile"
	"github.com/zadykian/ikeward/internal/routing"
)

// installLong is a variable, as it shows routing.MinBits.
var installLong = `Install the VPN from the client profile, the zip FILE, with the remote
network's values, as root:

  - install strongSwan's packages with apt-get, where they are missing, and
    where that fails, update the package lists and try once more;
  - write the files that differ from what the profile gives, naming each: the
    swanctl connection, the certificate, the CA, the key, which only root may
    read, strongSwan's drop-in, networkd's link ` + files.Link + `, the tunnel's, and its
    settings, the watchdog's units, the routing's, ` + routing.Unit + `,
    and ` + files.SettingsFile + `, which holds the remote network's values.
    ` + install.Record + ` lists them for uninstall, and install removes
    those it listed but no longer writes;
  - reload systemd and networkd, and enable the routing's unit, strongSwan and
    the watchdog's timer. Restart the routing's unit, which makes what is
    missing of ikeward's rules and floors, and the NAT table anew, before
    strongSwan starts, as routing --help says. Then load the connection.
    networkd or strongSwan restarts where its own settings changed, as it
    reads them only as it starts. Where install stops half way, a rerun
    restarts the daemon all the same: ` + install.Pending + `
    names it from before its settings change until install ends.

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
but for those only root may read, and changes nothing.`

// changesSettings is what install and uninstall do that calls for root.
const changesSettings = "changes the system's settings"

// newInstall is ikeward install.
func newInstall(sys system) *cobra.Command {
	var path string
	var given flags
	var dryRun bool
	command := &cobra.Command{
		Use: "install --profile FILE --dns ADDR --domain NAME... [--probe NAME]\n" +
			"      [--floor CIDR]... [--dry-run]",
		Short:                 "Install the VPN from the client profile",
		Long:                  installLong,
		Args:                  noArgs,
		DisableFlagsInUseLine: true,
		RunE: func(command *cobra.Command, _ []string) error {
			if path == "" {
				return usageError{errors.New("install needs --profile FILE"), command}
			}
			settings, err := given.settings()
			if err != nil {
				return usageError{err, command}
			}
			if err := sys.asRoot("install", changesSettings); err != nil {
				return err
			}
			if err := sys.asBinary(command.OutOrStdout(), dryRun); err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("cannot read the profile: %w", err)
			}
			p, err := profile.Parse(filepath.Base(path), data, sys.password)
			if err != nil {
				return err
			}
			if err := sys.clearOfServer(command, p.Server, settings.Floors); err != nil {
				return err
			}
			return sys.installer(command, dryRun).Install(p, settings)
		},
	}
	command.Flags().StringVar(&path, "profile", "", "the client profile, the zip `FILE`")
	command.Flags().StringVar(&given.dns, "dns", "", "the remote network's resolver, an IPv4 `ADDR`")
	command.Flags().StringArrayVar(&given.domains, "domain", nil,
		"a domain `NAME` of the remote network's, once for each")
	command.Flags().StringVar(&given.probe, "probe", "",
		"a `NAME` the resolver answers (default the first --domain)")
	command.Flags().StringArrayVar(&given.floors, "floor", nil,
		"a `CIDR` kept off the uplink from boot on, once for each")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "print the changes, and change nothing")
	return command
}

// flags are install's flags that give the remote network's values.
type flags struct {
	dns, probe      string
	domains, floors []string
}

// settings are the settings f gives, or an error that says what is wrong with them. A domain may
// be written in any case, and with a dot at its end. A floor that routing.Refusal refuses with no
// server's address, one wider than routing.MinBits, is wrong.
func (f flags) settings() (files.Settings, error) {
	if f.dns == "" || len(f.domains) == 0 {
		return files.Settings{}, errors.New("install needs --dns ADDR and --domain NAME")
	}
	var s files.Settings
	var errs []error
	dns, err := netip.ParseAddr(f.dns)
	if err != nil {
		errs = append(errs, fmt.Errorf("--dns %q is no address", f.dns))
	}
	s.DNS = dns.Unmap()
	for _, d := range f.domains {
		s.Domains = append(s.Domains, domainName(d))
	}
	s.Probe = s.Domains[0]
	if f.probe != "" {
		s.Probe = domainName(f.probe)
	}
	for _, floor := range f.floors {
		p, err := netip.ParsePrefix(floor)
		if err != nil {
			errs = append(errs, fmt.Errorf("--floor %q is no range", floor))
			continue
		}
		if why := routing.Refusal(p, nil); why != "" {
			errs = append(errs, fmt.Errorf("--floor %s: %s", p, why))
		}
		s.Floors = append(s.Floors, p)
	}
	if len(errs) == 0 {
		errs = append(errs, s.Check())
	}
	return s, errors.Join(errs...)
}

// clearOfServer refuses, as a mistake on command's line, a floor that holds an address of server,
// the VPN server the profile names. Rule 32001 would take charon's packets there to the floor, and
// the tunnel would never come up to let updown record the address. With no floor, it asks nothing.
func (s system) clearOfServer(command *cobra.Command, server string, floors []netip.Prefix) error {
	if len(floors) == 0 {
		return nil
	}
	addrs, err := s.serverAddrs(command.Context(), server)
	if err != nil {
		return err
	}
	var errs []error
	for _, floor := range floors {
		if why := routing.Refusal(floor, addrs); why != "" {
			errs = append(errs, fmt.Errorf("--floor %s: %s", floor, why))
		}
	}
	if len(errs) > 0 {
		return usageError{errors.Join(errs...), command}
	}
	return nil
}

// serverAddrs are the addresses of server: itself where server is an address, or else those its
// name resolves to now.
func (s system) serverAddrs(ctx context.Context, server string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(server); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	addrs, err := s.names.IPv4(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %s, the VPN server, whose address no --floor may "+
			"hold: %w", server, err)
	}
	return addrs, nil
}

// domainName is name in lower case, without a dot at its end.
func domainName(name string) string {
	return strings.TrimSuffix(strings.ToLower(name), ".")
}

// installer is install's and uninstall's view of sys, saying what they do on command's output.
func (s system) installer(command *cobra.Command, dryRun bool) install.System {
	return install.System{Files: s.files, Runner: s.runner, Tables: s.nat, Network: s.network,
		Out: command.OutOrStdout(), DryRun: dryRun}
}
