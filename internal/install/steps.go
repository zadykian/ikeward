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

// step is a command that has the system take up install's files. Install takes it only where due:
// where it changes a file the step reads, an earlier install owes it, or its probe finds the
// system needs it. A step with no probe runs on every install.
type step struct {
	command []string
	// reads are the files the step takes up: paths, and directories that end in a slash.
	reads []string
	// restart replaces command where install changes a file of own: a daemon's own settings, which
	// it reads only as it starts.
	restart, own []string
	// probe says why the system needs the step, or nothing where it does not.
	probe func(*probes) string
}

// steps are install's steps, in the order they run.
var steps = []step{
	{command: []string{"systemctl", "daemon-reload"}, reads: []string{unitDir},
		probe: (*probes).reloaded},
	{command: []string{"networkctl", "reload"}, reads: []string{"/etc/systemd/network/"},
		restart: []string{"systemctl", "restart", networkd},
		own:     []string{"/etc/systemd/networkd.conf.d/"}, probe: (*probes).network},
	{command: []string{"systemctl", "enable", routing.Unit, StrongSwan, Timer},
		reads: []string{unitDir + routing.Unit, unitDir + Timer}, probe: (*probes).enabled},
	// The service adds what is missing of the rules and the floors, and makes the NAT table
	// anew. No traffic to the remote network then takes the default route as the tunnel comes up.
	{command: []string{"systemctl", "restart", routing.Unit},
		reads: []string{unitDir + routing.Unit, files.SettingsFile}, probe: (*probes).routing},
	{command: []string{"systemctl", "start", StrongSwan},
		restart: []string{"systemctl", "restart", StrongSwan},
		own:     []string{"/etc/strongswan.d/"}, probe: (*probes).strongSwan},
	// strongSwan loads the files under /etc/swanctl as it starts, but not as it runs. A change
	// of the networks replaces the CHILD SA in the same IKE SA: the tunnel pauses for a moment.
	{command: []string{"swanctl", "--load-all", "--noprompt"}, reads: []string{"/etc/swanctl/"}},
	// Last, once the connection is loaded, which the watchdog checks.
	{command: []string{"systemctl", "start", Timer}, reads: []string{unitDir + Timer},
		probe: (*probes).timer},
}

// due is why a step is due, and whether its daemon restarts.
type due struct {
	why     string
	restart bool
}

// plan holds why each of steps is due, in turn, or nil for a step not due.
type plan []*due

// owedWhy is why a step is due that Journal or Pending owes.
const owedWhy = "an earlier install stopped before it ended"

// plan works out the steps due: those owed names, those that read a file of changed, and those
// whose probe finds that the system needs them.
func (System) plan(p *probes, changed []string, owed owed) plan {
	out := make(plan, len(steps))
	for i, st := range steps {
		out[i] = st.due(p, changed, owed)
	}
	return out
}

// due is why st is due, or nil for a step not due. A probe runs only where nothing else makes st
// due.
func (st step) due(p *probes, changed []string, owed owed) *due {
	if st.restart != nil {
		if owed.has(st.restart) {
			return &due{why: owedWhy, restart: true}
		}
		if path, ok := changedIn(changed, st.own); ok {
			return &due{why: path + " changes", restart: true}
		}
	}
	if owed.has(st.command) {
		return &due{why: owedWhy}
	}
	if path, ok := changedIn(changed, st.reads); ok {
		return &due{why: path + " changes"}
	}
	if st.probe == nil {
		return nil
	}
	if why := st.probe(p); why != "" {
		return &due{why: why}
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

// due is whether any step is due.
func (p plan) due() bool {
	return slices.ContainsFunc(p, func(d *due) bool { return d != nil })
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

// take runs the steps that plan holds due, in order, saying why each is, or in a dry run says it
// would. A step with no probe runs all the same, with no reason given.
func (s System) take(out *printer, plan plan) error {
	for i, d := range plan {
		if d == nil && steps[i].probe == nil {
			d = &due{}
		}
		if d == nil {
			continue
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
