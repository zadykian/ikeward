// Command ikeward keeps a Linux server on an IKEv2 VPN, from the client profile that hwdsl2's
// ikev2setup.sh makes. It sets up strongSwan, the remote network's DNS, the routing and a watchdog.
// Every packet goes to the server's own networks first, then to the remote network through the
// tunnel, then by the default route. While the tunnel is down, nothing for the remote network
// leaves. The command line is here, on cobra.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/host"
)

// version is what --version prints; make build sets it with -ldflags.
var version = "dev"

// main is the only place ikeward exits: every error comes back here.
func main() {
	if err := run(os.Args[1:], live()); err != nil {
		fmt.Fprintf(os.Stderr, "ikeward: %v\n", err)
		os.Exit(exitStatus(err))
	}
}

// live is the system ikeward runs on.
func live() system {
	return system{
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		terminal: host.Terminal{File: os.Stdin},
		files:    host.Root("/"), runner: host.Commands{},
		tunnel: host.VICI{Socket: host.VICISocket}, charon: host.VICI{Socket: host.VICISocket},
		network:  host.Netlink{},
		resolver: host.DNS{Link: files.Link},
		clock:    host.Clock{}, route: host.Netlink{}, nat: host.NAT{}, env: os.Getenv,
		euid:         os.Geteuid,
		runsAs:       host.RunsAs,
		names:        host.Names{},
		updownTunnel: host.VICI{Socket: host.VICISocket, NoBlock: true},
	}
}

// usageError is a mistake on ikeward's command line, in command, whose help it points to.
type usageError struct {
	error
	command *cobra.Command
}

func (e usageError) Error() string {
	return e.error.Error() + " (see " + e.command.CommandPath() + " --help)"
}

// watchdogError is a failure of the watchdog's own, not a check's: its service counts exit
// status 1 as success, which this must not be.
type watchdogError struct{ error }

// exitStatus is 2 for a mistake on the command line, 3 for a failure of the watchdog's own, and 1
// for any other error.
func exitStatus(err error) int {
	switch {
	case errors.As(err, &usageError{}):
		return 2
	case errors.As(err, &watchdogError{}):
		return 3
	}
	return 1
}
