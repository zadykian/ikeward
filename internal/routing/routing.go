// Package routing is ikeward routing, which ikeward-routing.service runs as the server boots,
// before any network comes up and before strongSwan. Every packet then follows one order: the
// server's own networks, then the remote network's, then the default route. A container's and a
// forwarded network's packets follow it too, and neither side's addresses depend on the other's.
// Rules sets the order. Main comes first, but for its default route, then files.Table. There
// ikeward updown routes each range the VPN server grants through files.Link while the tunnel is up.
//
// The floors, unreachable routes in files.Table, take the ranges files.SettingsFile gives and those
// updown learned while no route through files.Link does. While the tunnel is down, their traffic
// goes nowhere, rather than by the default route in the clear. NATTable masquerades what leaves by
// files.Link, whatever its source. The watchdog makes each again where its check fails. The package
// reaches the kernel through Kernel and NAT, and the files through Reader, which tests fake.
package routing

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/zadykian/ikeward/internal/files"
)

const (
	// Unit is the service that runs ikeward routing as the server boots.
	Unit = files.Routing + ".service"
	// Main is the kernel's main routing table, which holds the routes to the server's own networks
	// and its default route.
	Main = 254
	// FloorMetric is the floors' metric. updown's route through files.Link, of metric 0, comes
	// before its range's floor, and a replace of the route leaves the floor alone.
	FloorMetric = 4000
	// NATTable is ikeward's nftables table, of the ip family. It holds NATChain, and nothing else.
	NATTable = "ikeward"
	// NATChain is the table's NAT chain on the postrouting hook, at NATPriority: before Docker's
	// NAT rules, at srcnat, 100. The first NAT rule that matches a connection decides its source.
	NATChain    = "postrouting"
	NATPriority = 99
	// NATRule is the chain's one rule, in nft's syntax. It gives what leaves by files.Link the
	// address on it, the one the server assigned, which the tunnel's SAs take alone.
	NATRule = `oifname "` + files.Link + `" counter masquerade`
)

// Rule is an IPv4 routing rule that matches every packet. At Priority, it looks up Table, and
// passes over a route there no longer than SuppressPrefixlen bits, where that is 0 or more.
type Rule struct {
	Priority, Table, SuppressPrefixlen int
}

// String is r as ip rule lists it.
func (r Rule) String() string {
	table := strconv.Itoa(r.Table)
	if r.Table == Main {
		table = "main"
	}
	s := fmt.Sprintf("%d: from all lookup %s", r.Priority, table)
	if r.SuppressPrefixlen >= 0 {
		s += " suppress_prefixlength " + strconv.Itoa(r.SuppressPrefixlen)
	}
	return s
}

// Rules are ikeward's rules, in order, after the kernel's rule for the local table, and before its
// rule for main, at 32766. Each passes over a default route: main's goes last, and one that
// files.Table may hold for the VPN server never takes the server's own traffic.
var Rules = []Rule{
	{Priority: 32000, Table: Main, SuppressPrefixlen: 0},
	{Priority: 32001, Table: files.Table, SuppressPrefixlen: 0},
}

// Kernel is the kernel's IPv4 rules, and the floors in files.Table.
type Kernel interface {
	// Rules returns the IPv4 routing rules.
	Rules() ([]Rule, error)
	// AddRule adds rule, where no rule like it exists.
	AddRule(rule Rule) error
	// RemoveRule removes rule, where it exists.
	RemoveRule(rule Rule) error
	// SetFloor makes dst unreachable in files.Table at FloorMetric, in place of any route there
	// to dst of that metric.
	SetFloor(dst netip.Prefix) error
}

// NAT is NATTable, in nftables.
type NAT interface {
	// Set makes NATTable hold NATChain and NATRule alone, in one transaction. It deletes NATTable,
	// where it exists, with all it holds, then makes it again, so that the rule's counter starts
	// from 0.
	Set() error
}

// Missing is an error that says NATTable is not in place: the table is missing, or holds more or
// other than NATChain and NATRule. An error that says only that NATTable cannot be read is none.
type Missing struct{ Err error }

func (e Missing) Error() string { return e.Err.Error() }

func (e Missing) Unwrap() error { return e.Err }

// System is what routing changes, and Files holds files.SettingsFile and Learned.
type System struct {
	Kernel Kernel
	NAT    NAT
	Files  Reader
	// Out is where it says what it did.
	Out io.Writer
}

// Run makes NATTable, the floors and the rules, and says so. The rules come last, as nothing looks
// up the routes through files.Link or the floors before them. Where a part fails, Run goes on with
// the next, as the rules alone put the server's own networks first, and returns the errors.
func (s System) Run() error {
	var errs []error
	for _, set := range []func() error{s.SetNAT, s.SetFloors, s.SetRules} {
		errs = append(errs, set())
	}
	return errors.Join(errs...)
}

// SetNAT makes NATTable anew, and says so.
func (s System) SetNAT() error {
	if err := s.NAT.Set(); err != nil {
		return fmt.Errorf("cannot make table ip %s: %w", NATTable, err)
	}
	return s.printf("Made table ip %s: %s\n", NATTable, NATRule)
}

// SetFloors makes each of Floors unreachable in files.Table, those of files.SettingsFile first,
// then those of Learned, and says so. It names each range it passes over once, with the files
// that hold it, as a warning. Where one file cannot be read, it makes the other's floors all the
// same, then fails.
func (s System) SetFloors() error {
	settings, settingsErr := ReadSettings(s.Files)
	learned, learnedErr := ReadLearned(s.Files)
	floors, passed := learned.Floors(settings.Floors)
	for _, p := range passed {
		var in []string
		if slices.Contains(settings.Floors, p.Range) {
			in = append(in, files.SettingsFile)
		}
		if slices.Contains(learned.Ranges, p.Range) {
			in = append(in, Learned)
		}
		if err := s.printf("Passed over %s in %s: %s\n", p.Range, strings.Join(in, " and "),
			p.Why); err != nil {
			return err
		}
	}
	for _, dst := range floors {
		if err := s.Kernel.SetFloor(dst); err != nil {
			return fmt.Errorf("cannot make %s unreachable in table %d: %w", dst, files.Table, err)
		}
		if err := s.printf("Made %s unreachable in table %d: unreachable %s metric %d\n", dst,
			files.Table, dst, FloorMetric); err != nil {
			return err
		}
	}
	return errors.Join(settingsErr, learnedErr)
}

// SetRules adds each of Rules that is missing, then removes any other rule that looks up
// files.Table, as strongSwan's own did, at priority 220, before main. Each change is said. A rule
// of Rules goes in before another goes, so that no packet meanwhile finds neither.
func (s System) SetRules() error {
	rules, err := s.Kernel.Rules()
	if err != nil {
		return fmt.Errorf("cannot read the rules: %w", err)
	}
	for _, rule := range Rules {
		if slices.Contains(rules, rule) {
			continue
		}
		if err := s.Kernel.AddRule(rule); err != nil {
			return fmt.Errorf("cannot add rule %s: %w", rule, err)
		}
		if err := s.printf("Added rule %s\n", rule); err != nil {
			return err
		}
	}
	for _, rule := range Foreign(rules) {
		if err := s.Kernel.RemoveRule(rule); err != nil {
			return fmt.Errorf("cannot remove rule %s: %w", rule, err)
		}
		if err := s.printf("Removed rule %s, which looked up table %d too\n", rule,
			files.Table); err != nil {
			return err
		}
	}
	return nil
}

// Foreign are those of rules that look up files.Table but are not of Rules, in their order.
func Foreign(rules []Rule) []Rule {
	var foreign []Rule
	for _, rule := range rules {
		if rule.Table == files.Table && !slices.Contains(Rules, rule) {
			foreign = append(foreign, rule)
		}
	}
	return foreign
}

// printf writes to Out.
func (s System) printf(format string, args ...any) error {
	_, err := fmt.Fprintf(s.Out, format, args...)
	return err
}
