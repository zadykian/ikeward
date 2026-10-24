package install

import (
	"slices"
	"strings"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// unitDir holds the units install writes, and networkd the unit of systemd-networkd.
const (
	unitDir  = "/etc/systemd/system/"
	networkd = "systemd-networkd.service"
)

// step is a command that has the system take up install's files. A step is due where install
// changes a file it reads, or Pending owes its daemon's restart. Install takes each on every run
// all the same, and so mends an install that stopped half way.
type step struct {
	command []string
	// reads are the files the step takes up: paths, and directories that end in a slash.
	reads []string
	// restart replaces command where install changes a file of own: a daemon's own settings, which
	// it reads only as it starts.
	restart, own []string
}

// steps are install's steps, in the order they run.
var steps = []step{
	{command: []string{"systemctl", "daemon-reload"}, reads: []string{unitDir}},
	{command: []string{"networkctl", "reload"}, reads: []string{"/etc/systemd/network/"},
		restart: []string{"systemctl", "restart", networkd},
		own:     []string{"/etc/systemd/networkd.conf.d/"}},
	{command: []string{"systemctl", "enable", routing.Unit, StrongSwan, Timer},
		reads: []string{unitDir + routing.Unit, unitDir + Timer}},
	// The service adds what is missing of the rules and the floors, and makes the NAT table
	// anew. No traffic to the remote network then takes the default route as the tunnel comes up.
	{command: []string{"systemctl", "restart", routing.Unit},
		reads: []string{unitDir + routing.Unit, files.SettingsFile}},
	{command: []string{"systemctl", "start", StrongSwan},
		restart: []string{"systemctl", "restart", StrongSwan},
		own:     []string{"/etc/strongswan.d/"}},
	// strongSwan loads the files under /etc/swanctl as it starts, but not as it runs. A change
	// of the networks replaces the CHILD SA in the same IKE SA: the tunnel pauses for a moment.
	{command: []string{"swanctl", "--load-all", "--noprompt"}, reads: []string{"/etc/swanctl/"}},
	// Last, once the connection is loaded, which the watchdog checks.
	{command: []string{"systemctl", "start", Timer}, reads: []string{unitDir + Timer}},
}

// due is why a step is due, and whether its daemon restarts.
type due struct {
	why     string
	restart bool
}

// plan holds why each of steps is due, in turn, or nil for a step not due.
type plan []*due

// owedWhy is why a step is due that Pending owes.
const owedWhy = "an earlier install stopped before it ended"

// plan works out the steps due: the restarts of the daemons that pending names, and the steps
// that read a file of changed.
func (System) plan(changed, pending []string) plan {
	out := make(plan, len(steps))
	for i, st := range steps {
		out[i] = st.due(changed, pending)
	}
	return out
}

// due is why st is due, or nil for a step not due.
func (st step) due(changed, pending []string) *due {
	if st.restart != nil {
		if slices.Contains(pending, st.restart[len(st.restart)-1]) {
			return &due{why: owedWhy, restart: true}
		}
		if path, ok := changedIn(changed, st.own); ok {
			return &due{why: path + " changes", restart: true}
		}
	}
	if path, ok := changedIn(changed, st.reads); ok {
		return &due{why: path + " changes"}
	}
	return nil
}

// commandFor is the command st runs where d makes it due.
func (st step) commandFor(d *due) []string {
	if d.restart {
		return st.restart
	}
	return st.command
}

// lines are the commands of the steps due, each as a line.
func (p plan) lines() []string {
	var lines []string
	for i, d := range p {
		if d != nil {
			lines = append(lines, strings.Join(steps[i].commandFor(d), " "))
		}
	}
	return lines
}

// take runs the steps, in order, saying why each that plan holds due is, or in a dry run says it
// would. A step not due runs all the same, with no reason given.
func (s System) take(out *printer, plan plan) error {
	for i, d := range plan {
		if d == nil {
			d = &due{}
		}
		if err := s.runFor(out, steps[i].commandFor(d), d.why); err != nil {
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
