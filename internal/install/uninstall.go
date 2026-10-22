package install

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// Uninstall stops and disables strongSwan and the watchdog's timer, and stops a run of the
// watchdog, where systemd has them, then the routing's service. It removes routing.NATTable, the
// rules, files.Table's routes, the files Record lists, LastRestart, Pending and routing.Learned.
// It then has systemd and networkd drop them, and removes files.Link. The packages stay.
func (s System) Uninstall() error {
	out := &printer{w: s.Out}
	paths, err := s.record()
	if errors.Is(err, fs.ErrNotExist) {
		out.printf("Nothing to remove: there is no %s, which install writes\n", Record)
		return out.err
	}
	if err != nil {
		return err
	}
	for _, p := range []string{LastRestart, Pending, routing.Learned} {
		if _, _, err := s.Files.Read(p); !errors.Is(err, fs.ErrNotExist) {
			paths = append(paths, p)
		}
	}
	// strongSwan stops before its files go, closing the tunnel as it does, and the watchdog
	// before it, which would otherwise start it again. disable does nothing to the static
	// service, and --now stops it. The routing's service stops once strongSwan has; what it made
	// stays until uninstall removes it.
	for _, units := range [][]string{s.units(Timer, Service, StrongSwan), s.units(routing.Unit)} {
		if len(units) == 0 {
			continue
		}
		if err := s.run(out, "systemctl", append([]string{"disable", "--now"}, units...)...); err != nil {
			return err
		}
	}
	// Once strongSwan has stopped, no updown sets the route again. Traffic to the remote
	// network then leaves as on a server without ikeward.
	if err := s.removeRouting(out); err != nil {
		return err
	}
	for _, p := range append(paths, Record) {
		if err := s.remove(out, p); err != nil {
			return err
		}
	}
	for _, command := range [][]string{{"systemctl", "daemon-reload"}, {"networkctl", "reload"}} {
		if err := s.run(out, command[0], command[1:]...); err != nil {
			return err
		}
	}
	// Last, once networkd no longer has its files, as it removes no link as it reloads. Its routes
	// go with it.
	if err := s.removeLink(out, files.Link); err != nil {
		return err
	}
	return out.err
}

// units are those of names that systemd has, in their order: systemctl disables none where one is
// missing. Install may have stopped before it wrote the watchdog's units or the routing's, or
// uninstall removed them already, and strongSwan's packages may be gone.
func (s System) units(names ...string) []string {
	var units []string
	for _, unit := range names {
		// It exits 1 where it lists no unit.
		if _, err := s.Runner.Run("systemctl", "list-unit-files", "--quiet", unit); err == nil {
			units = append(units, unit)
		}
	}
	return units
}

// removeRouting removes what ikeward routing and updown made: routing.NATTable, then the rules,
// then files.Table's routes, the floors among them.
func (s System) removeRouting(out *printer) error {
	if err := s.removeTable(out, routing.NATTable); err != nil {
		return err
	}
	rules, err := s.Network.Rules()
	if err != nil {
		return fmt.Errorf("cannot read the rules: %w", err)
	}
	for _, rule := range rules {
		if !slices.Contains(routing.Rules, rule) && rule.Table != files.Table {
			continue
		}
		if s.DryRun {
			out.printf("Would remove rule %s\n", rule)
			continue
		}
		if err := s.Network.RemoveRule(rule); err != nil {
			return fmt.Errorf("cannot remove rule %s: %w", rule, err)
		}
		out.printf("Removed rule %s\n", rule)
	}
	return s.removeRoutes(out)
}

// removeRoutes removes every route in files.Table, or in a dry run says it would.
func (s System) removeRoutes(out *printer) error {
	if s.DryRun {
		routes, err := s.Network.Routes(files.Table)
		if err != nil {
			return fmt.Errorf("cannot read table %d: %w", files.Table, err)
		}
		if len(routes) > 0 {
			out.printf("Would remove the routes of table %d: %d\n", files.Table, len(routes))
		}
		return nil
	}
	removed, err := s.Network.RemoveRoutes(files.Table)
	if err != nil {
		return fmt.Errorf("cannot remove the routes of table %d: %w", files.Table, err)
	}
	if removed > 0 {
		out.printf("Removed the routes of table %d: %d\n", files.Table, removed)
	}
	return nil
}

// removeTable removes table ip name, where nftables has it, or in a dry run says it would.
func (s System) removeTable(out *printer, name string) error {
	has, err := s.Tables.HasTable(name)
	switch {
	case err != nil:
		return fmt.Errorf("cannot tell whether there is a table ip %s: %w", name, err)
	case !has:
		return nil
	case s.DryRun:
		out.printf("Would remove table ip %s\n", name)
		return nil
	}
	if err := s.Tables.RemoveTable(name); err != nil {
		return fmt.Errorf("cannot remove table ip %s: %w", name, err)
	}
	out.printf("Removed table ip %s\n", name)
	return nil
}

// removeLink removes the link named name, where the kernel has it, or in a dry run says it would.
func (s System) removeLink(out *printer, name string) error {
	has, err := s.Network.HasLink(name)
	switch {
	case err != nil:
		return fmt.Errorf("cannot tell whether there is a link %s: %w", name, err)
	case !has:
		return nil
	case s.DryRun:
		out.printf("Would remove link %s\n", name)
		return nil
	}
	if err := s.Network.RemoveLink(name); err != nil {
		return fmt.Errorf("cannot remove link %s: %w", name, err)
	}
	out.printf("Removed link %s\n", name)
	return nil
}
