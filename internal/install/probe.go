package install

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// probes look at the system for the steps, and change nothing. A probe that fails to look says
// why, which makes its step due. units keeps what systemctl show said of each unit asked.
type probes struct {
	s        System
	settings files.Settings
	units    map[string]unitState
}

func newProbes(s System, settings files.Settings) *probes {
	return &probes{s: s, settings: settings, units: map[string]unitState{}}
}

// unitState is what systemctl show says of a unit: its load, active and unit file states, and
// whether its files changed since systemd read them. err is why systemctl could not say.
type unitState struct {
	load, active, file, changed string
	err                         error
}

// unit is what systemctl show says of the unit named name, asked once.
func (p *probes) unit(name string) unitState {
	if u, ok := p.units[name]; ok {
		return u
	}
	out, err := p.s.Runner.Run("systemctl", "show",
		"--property=LoadState,ActiveState,UnitFileState,NeedDaemonReload", name)
	values := map[string]string{}
	for line := range strings.Lines(out) {
		key, value, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		values[key] = value
	}
	u := unitState{load: values["LoadState"], active: values["ActiveState"],
		file: values["UnitFileState"], changed: values["NeedDaemonReload"], err: err}
	p.units[name] = u
	return u
}

// state says why the unit named name differs from want, in the field of unitState that has
// returns: "ikeward-watchdog.timer is disabled". Where the field holds want, it says nothing.
func (p *probes) state(name, want string, has func(unitState) string) string {
	u := p.unit(name)
	if u.err != nil {
		return fmt.Sprintf("systemctl show %s fails: %v", name, u.err)
	}
	if got := has(u); got != want {
		if got == "" {
			got = "not " + want
		}
		return name + " is " + got
	}
	return ""
}

// reloaded says why systemd must read its units again: a unit's files changed since it read them,
// or one of ikeward's units is not loaded.
func (p *probes) reloaded() string {
	for _, name := range []string{routing.Unit, StrongSwan, Timer, Service} {
		if u := p.unit(name); u.err == nil && u.changed == "yes" {
			return name + " changed on disk"
		}
		if name == StrongSwan {
			continue
		}
		if why := p.state(name, "loaded", func(u unitState) string { return u.load }); why != "" {
			return why
		}
	}
	return ""
}

// enabled says why systemctl must enable the units: one is not enabled.
func (p *probes) enabled() string {
	for _, name := range []string{routing.Unit, StrongSwan, Timer} {
		if why := p.state(name, "enabled", func(u unitState) string { return u.file }); why != "" {
			return why
		}
	}
	return ""
}

// active says why the unit named name must start: its state, where not active.
func (p *probes) active(name string) string {
	return p.state(name, "active", func(u unitState) string { return u.active })
}

func (p *probes) strongSwan() string { return p.active(StrongSwan) }

func (p *probes) timer() string { return p.active(Timer) }

// checks is status's view of the system, whose checks read what the steps make.
func (p *probes) checks() status.System {
	return status.System{Network: p.s.Network, Runner: p.s.Runner, Files: p.s.Files}
}

// network says why networkd must read its files again: files.Link is missing, or lacks its own
// address. Or the DNS check fails, of the settings networkd gives resolved.
func (p *probes) network() string {
	has, err := p.s.Network.HasLink(files.Link)
	switch {
	case err != nil:
		return fmt.Sprintf("the kernel cannot say whether there is a link %s: %v", files.Link, err)
	case !has:
		return "there is no link " + files.Link
	}
	addrs, err := p.s.Network.Addrs(files.Link)
	if err != nil {
		return fmt.Sprintf("the addresses of %s cannot be read: %v", files.Link, err)
	}
	own := netip.MustParsePrefix(files.LinkULA).Addr()
	if !slices.ContainsFunc(addrs, func(a netip.Prefix) bool { return a.Addr() == own }) {
		return files.Link + " lacks its own address, " + files.LinkULA
	}
	if r := p.checks().CheckDNS(p.settings); !r.OK {
		return "the DNS check fails: " + r.Detail
	}
	return ""
}
