package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const rootLong = `Keep this server on an IKEv2 VPN, from the client profile that hwdsl2's
ikev2setup.sh makes: strongSwan, the remote network's DNS, the routing and a
watchdog. Every packet goes to the server's own networks first, then to the
remote network through the tunnel, then by the default route. A container's
and a forwarded network's do too. While the tunnel is down, nothing for the
remote network leaves.`

// run runs ikeward's command line on args, on sys.
func run(args []string, sys system) error {
	root := commandLine(sys)
	// Never nil, for which cobra would read os.Args instead.
	root.SetArgs(append([]string{}, args...))
	return root.Execute()
}

// commandLine is ikeward's commands. cobra checks the root's arguments only when it runs, so the
// root prints its help itself rather than leave it to cobra, which would take any argument.
func commandLine(sys system) *cobra.Command {
	root := &cobra.Command{
		Use:     "ikeward",
		Long:    rootLong,
		Version: version,
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
		// main prints the error, and the usage stays out of it.
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	// Nothing calls for shell completion, and a binary run as root takes no command it does not
	// document. cobra still adds its hidden __complete, which only lists completions.
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(sys.stdout)
	root.SetFlagErrorFunc(func(command *cobra.Command, err error) error {
		return usageError{err, command}
	})
	root.SetHelpCommand(newHelp())
	root.AddCommand(newInstall(sys), newUninstall(sys), newStatus(sys), newWatchdog(sys),
		newUpdown(sys), newRouting(sys))
	return root
}

// newHelp is ikeward help [COMMAND]. cobra's own prints the root's help for a topic it does not
// know and succeeds, where a mistake on the command line should fail.
func newHelp() *cobra.Command {
	return &cobra.Command{
		Use:   "help [COMMAND]",
		Short: "Help about any command",
		Args: func(command *cobra.Command, args []string) error {
			if _, rest, err := command.Root().Find(args); err != nil || len(rest) > 0 {
				return usageError{fmt.Errorf("unknown help topic %q",
					strings.Join(args, " ")), command}
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			topic, _, err := command.Root().Find(args)
			if err != nil {
				return err
			}
			// cobra adds them only to the command it runs, as cobra's own help does here.
			topic.InitDefaultHelpFlag()
			topic.InitDefaultVersionFlag()
			return topic.Help()
		},
	}
}

// noArgs refuses any argument, as a mistake on the command line.
func noArgs(command *cobra.Command, args []string) error {
	if err := cobra.NoArgs(command, args); err != nil {
		return usageError{err, command}
	}
	return nil
}
