package main

import (
	"github.com/spf13/cobra"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
)

const uninstallLong = `Remove what install wrote, as root: stop and disable strongSwan and the
watchdog's timer, and stop a run of the watchdog, where systemd has them,
then the routing's unit, ` + routing.Unit + `. Remove the nftables table
ip ` + routing.NATTable + `, ikeward's rules and any other rule of table 220, and
the routes of table 220, the floors among them. Remove the files that
` + install.Record + ` lists,
` + install.LastRestart + ` where the watchdog wrote it,
` + routing.Learned + ` where updown wrote it, and
` + install.Journal + ` and ` + install.Pending + `
where an install left them, then reload systemd and networkd. Remove the link
` + files.Link + `, the tunnel's, last, as networkd removes no link as it reloads.
The packages stay. Traffic to the remote network's ranges then leaves as on a
server without ikeward. networkd keeps its own settings until it next starts,
as it reads them only as it starts.

--dry-run prints what uninstall would remove and run, and changes nothing.`

// newUninstall is ikeward uninstall.
func newUninstall(sys system) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:                   "uninstall [--dry-run]",
		Short:                 "Remove what install wrote",
		Long:                  uninstallLong,
		Args:                  noArgs,
		DisableFlagsInUseLine: true,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := sys.asRoot("uninstall", changesSettings); err != nil {
				return err
			}
			return sys.installer(command, dryRun).Uninstall()
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "print the changes, and change nothing")
	return command
}
