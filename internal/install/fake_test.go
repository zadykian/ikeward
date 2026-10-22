package install

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// file is a file of fakeFiles.
type file struct {
	data []byte
	mode fs.FileMode
}

// fakeFiles is a root directory in memory, which fails where told to.
type fakeFiles struct {
	files map[string]file
	// writes lists the paths written, in order, and removes those removed.
	writes, removes []string
	// failRead, failWrite and failRemove fail the operation on a path.
	failRead, failWrite, failRemove map[string]error
}

func newFakeFiles() *fakeFiles {
	return &fakeFiles{files: map[string]file{}, failRead: map[string]error{},
		failWrite: map[string]error{}, failRemove: map[string]error{}}
}

func (f *fakeFiles) Read(path string) ([]byte, fs.FileMode, error) {
	if err := f.failRead[path]; err != nil {
		return nil, 0, err
	}
	got, ok := f.files[path]
	if !ok {
		return nil, 0, fs.ErrNotExist
	}
	return slices.Clone(got.data), got.mode, nil
}

func (f *fakeFiles) Write(path string, data []byte, mode fs.FileMode) error {
	if err := f.failWrite[path]; err != nil {
		return err
	}
	f.writes = append(f.writes, path)
	f.files[path] = file{slices.Clone(data), mode}
	return nil
}

func (f *fakeFiles) Remove(path string) error {
	if err := f.failRemove[path]; err != nil {
		return err
	}
	if _, ok := f.files[path]; !ok {
		return fs.ErrNotExist
	}
	f.removes = append(f.removes, path)
	delete(f.files, path)
	return nil
}

// paths are the paths of f's files, sorted.
func (f *fakeFiles) paths() []string {
	return slices.Sorted(maps.Keys(f.files))
}

// fakeRunner runs no command: it records each, answers dpkg-query from installed and systemctl
// list-unit-files from noUnit, and fails those that fail names.
type fakeRunner struct {
	commands []string
	// installed are the packages dpkg-query reports installed.
	installed map[string]bool
	// noUnit are the units systemd lacks.
	noUnit map[string]bool
	// fail fails the command whose line it names, and failOnce the first time it runs.
	fail, failOnce map[string]error
}

func newFakeRunner(installed ...string) *fakeRunner {
	r := &fakeRunner{installed: map[string]bool{}, noUnit: map[string]bool{},
		fail: map[string]error{}, failOnce: map[string]error{}}
	for _, name := range installed {
		r.installed[name] = true
	}
	return r
}

func (r *fakeRunner) Run(name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	r.commands = append(r.commands, line)
	if err := r.fail[line]; err != nil {
		return "", err
	}
	if err := r.failOnce[line]; err != nil {
		delete(r.failOnce, line)
		return "", err
	}
	if name == "systemctl" && args[0] == "list-unit-files" {
		unit := args[len(args)-1]
		if r.noUnit[unit] {
			return "", errors.New("exit status 1")
		}
		return unit + " enabled disabled\n", nil
	}
	if name == "dpkg-query" {
		pkg := args[len(args)-1]
		switch {
		case r.installed[pkg]:
			return "installed", nil
		case pkg == "libcharon-extra-plugins":
			// Removed, but not purged.
			return "config-files", nil
		}
		return "", fmt.Errorf("exit status 1: dpkg-query: no packages found matching %s", pkg)
	}
	return "", nil
}

// ran is r's commands but the queries: dpkg-query's and systemctl list-unit-files'.
func (r *fakeRunner) ran() []string {
	var ran []string
	for _, line := range r.commands {
		if !strings.HasPrefix(line, "dpkg-query ") &&
			!strings.HasPrefix(line, "systemctl list-unit-files ") {
			ran = append(ran, line)
		}
	}
	return ran
}

// fakeTables are nftables' tables, with those that tables names, which records those removed.
// hasErr fails HasTable, and removeErr RemoveTable.
type fakeTables struct {
	tables            map[string]bool
	removed           []string
	hasErr, removeErr error
}

func (f *fakeTables) HasTable(name string) (bool, error) { return f.tables[name], f.hasErr }

func (f *fakeTables) RemoveTable(name string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, name)
	delete(f.tables, name)
	return nil
}

// withNAT are tables with ikeward's NAT table, as ikeward routing makes it.
func withNAT() *fakeTables { return &fakeTables{tables: map[string]bool{"ikeward": true}} }

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("the pipe is closed") }

// checkLines fails t where got's lines are not want.
func checkLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n  %s\nwant:\n  %s", what, strings.Join(got, "\n  "),
			strings.Join(want, "\n  "))
	}
}

// fakeNetwork is a kernel with rules, and routes by table, which records the rules removed, and
// removes the routes of a table. It has the links that links names, and records those removed.
// The errors fail HasLink, RemoveLink, Rules, RemoveRule and RemoveRoutes, in turn.
type fakeNetwork struct {
	rules              []routing.Rule
	routes             map[int][]status.Route
	links              map[string]bool
	removed, unruled   []string
	linkErr, removeErr error
	rulesErr, ruleErr  error
	routesErr          error
}

func (f *fakeNetwork) HasLink(name string) (bool, error) { return f.links[name], f.linkErr }

func (f *fakeNetwork) RemoveLink(name string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, name)
	delete(f.links, name)
	return nil
}

func (f *fakeNetwork) Addrs(string) ([]netip.Prefix, error) {
	return nil, errors.New("install reads no address")
}

func (f *fakeNetwork) Rules() ([]routing.Rule, error) { return slices.Clone(f.rules), f.rulesErr }

func (f *fakeNetwork) RemoveRule(rule routing.Rule) error {
	if f.ruleErr != nil {
		return f.ruleErr
	}
	f.unruled = append(f.unruled, rule.String())
	f.rules = slices.DeleteFunc(f.rules, func(r routing.Rule) bool { return r == rule })
	return nil
}

func (f *fakeNetwork) Routes(table int) ([]status.Route, error) { return f.routes[table], nil }

func (f *fakeNetwork) RemoveRoutes(table int) (int, error) {
	if f.routesErr != nil {
		return 0, f.routesErr
	}
	n := len(f.routes[table])
	delete(f.routes, table)
	return n, nil
}

// home is a kernel as ikeward leaves it: the kernel's rules with ikeward's, and table 220's route
// and floor. It has the tunnel's link too, as networkd makes it once install has run.
func home() *fakeNetwork {
	subnet := settings.Floors[0]
	return &fakeNetwork{
		rules: []routing.Rule{{Priority: 0, Table: 255, SuppressPrefixlen: -1}, routing.Rules[0],
			routing.Rules[1], {Priority: 32766, Table: routing.Main, SuppressPrefixlen: -1}},
		routes: map[int][]status.Route{files.Table: {
			{Dst: subnet, Link: files.Link, Type: status.Unicast},
			{Dst: subnet, Type: status.Unreachable, Metric: routing.FloorMetric}}},
		links: map[string]bool{files.Link: true},
	}
}
