package main

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/watchdog"
)

const watchdogLong = `Check the VPN as status does, and where a check fails, restart
strongSwan, or make ikeward's routing again, as root. The timer
` + install.Timer + ` runs it every 5 minutes, from 3 minutes after boot.

While every check passes, watchdog prints nothing. Where one fails, it prints
those that fail and runs systemctl restart --no-block ` + install.StrongSwan + `,
unless it did so under 10 minutes ago, as ` + install.LastRestart + ` records.
After a restart, it checks again every 2 seconds, for 30 seconds at most, until
every check passes, or until only those a restart does not mend, below, fail
twice in a row.

Where the rule, floor or NAT check fails, watchdog first makes that part again,
as routing does, says so, and checks again: a restart of strongSwan makes none
of them. Where only the DNS, rule, floor or NAT check then fails, or the
resolver check with an answer, it leaves strongSwan as it is. A restart
changes neither ` + files.Link + `'s own address and resolved's settings, which networkd
gives, nor ikeward's rules, floors and table ip ` + routing.NATTable + `. Nor does it change an
answer of the remote network's resolver, as SERVFAIL: the answer came through
the tunnel, which works. Where the resolver does not answer, the watchdog
restarts strongSwan, as the tunnel may carry nothing.

Where the kernel refuses a part, watchdog goes on as above for the other
checks, restarting strongSwan where they need it, then exits 3.

watchdog exits 0 where every check passes, and 1 while one fails, which the
service ` + install.Service + ` counts as success: the VPN down is no
failure of the watchdog's. It exits 3 where it fails itself, as where it
cannot read or write ` + install.LastRestart + `, systemctl fails, or
the kernel refuses a part of the routing, which the service counts as a
failure.

To stop the VPN, stop the watchdog's timer and service with strongSwan, or the
watchdog starts it again:

  systemctl stop ` + install.Timer + ` ` + install.Service + ` \
    ` + install.StrongSwan

// newWatchdog is ikeward watchdog.
func newWatchdog(sys system) *cobra.Command {
	return &cobra.Command{
		Use:   "watchdog",
		Short: "Check the VPN; make missing routing, or restart strongSwan",
		Long:  watchdogLong,
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			err := sys.asRoot("watchdog", "restarts strongSwan")
			if err == nil {
				err = sys.watchdog(command.OutOrStdout()).Run(command.Context())
			}
			if err != nil && !errors.As(err, &watchdog.Unhealthy{}) {
				return watchdogError{err}
			}
			return err
		},
	}
}

// watchdog is the watchdog's view of s, saying what it does on out.
func (s system) watchdog(out io.Writer) watchdog.System {
	return watchdog.System{Checker: s.status(), Kernel: s.network, NAT: s.nat, Files: s.files,
		Runner: s.runner, Clock: s.clock, Out: out}
}
