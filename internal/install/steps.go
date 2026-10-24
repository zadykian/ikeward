package install

import (
	"slices"
	"strings"

	"github.com/zadykian/ikeward/internal/routing"
)

// networkd is the unit of systemd-networkd.
const networkd = "systemd-networkd.service"

// step is a command that has the system take up install's files. Install takes each on every run,
// and so mends an install that stopped half way.
type step struct {
	command []string
	// restart replaces command where install changes a file of own: a daemon's own settings, which
	// it reads only as it starts.
	restart, own []string
}

// steps are install's steps, in the order they run.
var steps = []step{
	{command: []string{"systemctl", "daemon-reload"}},
	{command: []string{"networkctl", "reload"},
		restart: []string{"systemctl", "restart", networkd},
		own:     []string{"/etc/systemd/networkd.conf.d/"}},
	{command: []string{"systemctl", "enable", routing.Unit, StrongSwan, Timer}},
	// The service adds what is missing of the rules and the floors, and makes the NAT table
	// anew. No traffic to the remote network then takes the default route as the tunnel comes up.
	{command: []string{"systemctl", "restart", routing.Unit}},
	{command: []string{"systemctl", "start", StrongSwan},
		restart: []string{"systemctl", "restart", StrongSwan},
		own:     []string{"/etc/strongswan.d/"}},
	// strongSwan loads the files under /etc/swanctl as it starts, but not as it runs. A change
	// of the networks replaces the CHILD SA in the same IKE SA: the tunnel pauses for a moment.
	{command: []string{"swanctl", "--load-all", "--noprompt"}},
	// Last, once the connection is loaded, which the watchdog checks.
	{command: []string{"systemctl", "start", Timer}},
}

// commandFor is the command st runs: its daemon's restart where restart names the daemon, whose
// settings changed in this run or one that stopped.
func (st step) commandFor(restart []string) []string {
	if st.restart != nil && slices.Contains(restart, st.restart[len(st.restart)-1]) {
		return st.restart
	}
	return st.command
}

// take runs the steps, in order, restarting the daemons that restart names, or in a dry run says
// it would.
func (s System) take(out *printer, restart []string) error {
	for _, st := range steps {
		command := st.commandFor(restart)
		if err := s.run(out, command[0], command[1:]...); err != nil {
			return err
		}
	}
	return nil
}

// changedIn returns the first path of changed that reads names, itself or by a directory that
// holds it.
func changedIn(changed, reads []string) (string, bool) {
	for _, p := range changed {
		if slices.ContainsFunc(reads, func(r string) bool {
			return p == r || strings.HasSuffix(r, "/") && strings.HasPrefix(p, r)
		}) {
			return p, true
		}
	}
	return "", false
}
