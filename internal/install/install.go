// Package install is ikeward install and uninstall.
//
// Install installs strongSwan's packages where they are missing, and writes the files
// internal/files makes from the profile and the settings where they differ. It then has systemd,
// networkd and strongSwan take them up, ikeward's routing before strongSwan. Uninstall removes
// install's files again, the rules, files.Table's routes, the NAT table and files.Link. Both reach
// the system through Files, Runner, Tables and Network, and with DryRun print what they would
// change, and change nothing.
package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/profile"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// Packages are what install has apt-get install where missing: strongSwan's daemon for systemd,
// swanctl, and charon's extra plugins.
var Packages = []string{"strongswan", "strongswan-swanctl", "charon-systemd",
	"libcharon-extra-plugins"}

const (
	// Record lists the files install wrote, for uninstall to remove.
	Record = "/var/lib/ikeward/installed"
	// LastRestart holds when ikeward watchdog last restarted strongSwan, which uninstall removes
	// too: install does not write it.
	LastRestart = "/var/lib/ikeward/last-restart"
	// Pending lists the daemons whose settings install changes, from before they change until
	// install ends, for a rerun where it stopped half way. Uninstall removes it too.
	Pending = "/var/lib/ikeward/pending-restarts"
	// StrongSwan is strongSwan's unit, which charon-systemd ships.
	StrongSwan = files.StrongSwan
	// Timer is the watchdog's timer, and Service the service it starts.
	Timer   = files.Watchdog + ".timer"
	Service = files.Watchdog + ".service"
)

// Files are the files under the system's root directory, each named by its absolute path. Their
// errors leave out the file's name.
type Files interface {
	// Read returns the file at path and its permission bits; where there is none, an error that
	// wraps fs.ErrNotExist.
	Read(path string) ([]byte, fs.FileMode, error)
	// Write replaces the file at path with data and mode, making its directory where missing.
	Write(path string, data []byte, mode fs.FileMode) error
	// Remove removes the file at path; where there is none, an error that wraps fs.ErrNotExist.
	Remove(path string) error
}

// Runner runs commands.
type Runner interface {
	// Run runs the program name with args and no input, and returns what it printed on stdout.
	Run(name string, args ...string) (string, error)
}

// Tables are nftables' tables of the ip family, of which uninstall removes routing.NATTable.
type Tables interface {
	// HasTable is whether nftables has the table named name.
	HasTable(name string) (bool, error)
	// RemoveTable removes the table named name, where it exists.
	RemoveTable(name string) error
}

// Network is the kernel's rules and routes, of which uninstall removes ikeward's, and its links,
// of which it removes files.Link.
type Network interface {
	status.Network
	// HasLink is whether the kernel has a link named name.
	HasLink(name string) (bool, error)
	// RemoveLink removes the link named name, where it exists, with its addresses and routes.
	RemoveLink(name string) error
	// RemoveRule removes rule, where it exists.
	RemoveRule(rule routing.Rule) error
	// RemoveRoutes removes every IPv4 route in table, and returns how many it removed.
	RemoveRoutes(table int) (int, error)
}

// System is what install and uninstall change.
type System struct {
	Files   Files
	Runner  Runner
	Tables  Tables
	Network Network
	// Out is where they say what they do.
	Out io.Writer
	// DryRun has them say what they would do, and do nothing that changes the system.
	DryRun bool
}

// printer writes to w, keeping the first error: a failed write stops nothing half done, and the
// command returns the error at its end.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) printf(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, args...)
	}
}

// Install installs the VPN from p, with settings, the remote network's values. It installs
// strongSwan's packages where missing, and writes the files that differ. It then runs the commands
// that take them up.
func (s System) Install(p *profile.Profile, settings files.Settings) error {
	generated, err := files.Generate(p, settings)
	if err != nil {
		return err
	}
	credentials, err := files.Credentials(p)
	if err != nil {
		return err
	}
	out := &printer{w: s.Out}
	out.printf("The profile gives:\n")
	for _, line := range p.Summary() {
		out.printf("  %s\n", line)
	}
	if missing := s.missing(); len(missing) > 0 {
		if err := s.installPackages(out, missing); err != nil {
			return err
		}
	}
	restart, err := s.pending()
	if err != nil {
		return err
	}
	if len(restart.units) > 0 {
		out.printf("An earlier install stopped before it ended: %s lists %s to restart\n",
			Pending, strings.Join(restart.units, " and "))
	}
	if err := s.write(out, append(generated, credentials...), restart); err != nil {
		return err
	}
	for _, command := range takeUp(restart.units) {
		if err := s.run(out, command[0], command[1:]...); err != nil {
			return err
		}
	}
	if err := s.restarted(restart); err != nil {
		return err
	}
	return out.err
}

// missing returns the packages of Packages that dpkg has not installed; one that dpkg-query does
// not know is missing too.
func (s System) missing() []string {
	var missing []string
	for _, name := range Packages {
		state, err := s.Runner.Run("dpkg-query", "--show", "--showformat=${db:Status-Status}",
			name)
		if err != nil || state != "installed" {
			missing = append(missing, name)
		}
	}
	return missing
}

// installPackages has apt-get install the packages missing. Where that fails, the package lists
// may be missing or out of date, as on a new server or once an update replaced a listed version.
// apt-get then updates them and tries once more.
func (s System) installPackages(out *printer, missing []string) error {
	// The lock's timeout waits out unattended-upgrades, which a new server runs at boot.
	args := append([]string{"install", "--yes", "-o", "DPkg::Lock::Timeout=300"}, missing...)
	err := s.run(out, "apt-get", args...)
	if err == nil {
		return nil
	}
	out.printf("Updating the package lists, as %v\n", err)
	if err := s.run(out, "apt-get", "update"); err != nil {
		return err
	}
	return s.run(out, "apt-get", args...)
}

// networkd is systemd-networkd's unit.
const networkd = "systemd-networkd.service"

// daemon is a unit, and the directory of its own settings.
type daemon struct{ unit, dir string }

// daemons are the units that read their own settings only as they start: install restarts one
// where it changes a file in its directory.
var daemons = []daemon{
	{networkd, "/etc/systemd/networkd.conf.d/"},
	{StrongSwan, "/etc/strongswan.d/"},
}

// takeUp is the commands that have the system take up install's files. Each runs on every install,
// and so mends an install that stopped half way. But a daemon that restart names, whose settings
// changed in this run or one that stopped, restarts instead. The routing's service restarts each
// time, before strongSwan starts.
func takeUp(restart []string) [][]string {
	orRestart := func(unit string, command ...string) []string {
		if slices.Contains(restart, unit) {
			return []string{"systemctl", "restart", unit}
		}
		return command
	}
	return [][]string{
		{"systemctl", "daemon-reload"},
		orRestart(networkd, "networkctl", "reload"),
		{"systemctl", "enable", routing.Unit, StrongSwan, Timer},
		// The service adds what is missing of the rules and the floors, and makes the NAT table
		// anew. No traffic to the remote network then takes the default route as the tunnel comes up.
		{"systemctl", "restart", routing.Unit},
		orRestart(StrongSwan, "systemctl", "start", StrongSwan),
		// strongSwan loads the files under /etc/swanctl as it starts, but not as it runs. A change
		// of the networks replaces the CHILD SA in the same IKE SA: the tunnel pauses for a moment.
		{"swanctl", "--load-all", "--noprompt"},
		// Last, once the connection is loaded, which the watchdog checks.
		{"systemctl", "start", Timer},
	}
}

// changedIn is whether changed holds a file in dir.
func changedIn(changed []string, dir string) bool {
	return slices.ContainsFunc(changed, func(p string) bool { return strings.HasPrefix(p, dir) })
}

// run runs the command name with args, or in a dry run says it would.
func (s System) run(out *printer, name string, args ...string) error {
	line := strings.Join(append([]string{name}, args...), " ")
	if s.DryRun {
		out.printf("Would run %s\n", line)
		return nil
	}
	out.printf("Running %s\n", line)
	if _, err := s.Runner.Run(name, args...); err != nil {
		return fmt.Errorf("%s: %w", line, err)
	}
	return nil
}

// remove removes the file at p, or in a dry run says it would. A file already gone is passed over.
func (s System) remove(out *printer, p string) error {
	if s.DryRun {
		out.printf("Would remove %s\n", p)
		return nil
	}
	err := s.Files.Remove(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("cannot remove %s: %w", p, err)
	}
	out.printf("Removed %s\n", p)
	return nil
}
