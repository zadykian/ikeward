package install

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/netip"
	"slices"
	"strings"

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
// list-unit-files from noUnit, and fails those that fail names. systemctl show says each unit is
// as install leaves it, but where states names a property.
type fakeRunner struct {
	commands []string
	// installed are the packages dpkg-query reports installed.
	installed map[string]bool
	// noUnit are the units systemd lacks.
	noUnit map[string]bool
	// states are properties of units, as "UNIT PROPERTY", and their values.
	states map[string]string
	// fail fails the command whose line it names, and failOnce the first time it runs.
	fail, failOnce map[string]error
}

func newFakeRunner(installed ...string) *fakeRunner {
	r := &fakeRunner{installed: map[string]bool{}, noUnit: map[string]bool{},
		fail: map[string]error{}, failOnce: map[string]error{}, states: map[string]string{}}
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
	if name == "systemctl" && args[0] == "show" {
		return r.show(args[len(args)-1]), nil
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

// show is what systemctl show prints of unit: what states names, else the unit as install leaves
// it, and the watchdog's service as its timer leaves it between runs.
func (r *fakeRunner) show(unit string) string {
	values := []string{"LoadState=loaded", "ActiveState=active", "UnitFileState=enabled",
		"NeedDaemonReload=no"}
	if unit == Service {
		values[1], values[2] = "ActiveState=inactive", "UnitFileState=static"
	}
	var out strings.Builder
	for _, v := range values {
		key, _, _ := strings.Cut(v, "=")
		if state, ok := r.states[unit+" "+key]; ok {
			v = key + "=" + state
		}
		out.WriteString(v + "\n")
	}
	return out.String()
}

// ran is r's commands but the queries: dpkg-query's, and systemctl list-unit-files' and show's.
func (r *fakeRunner) ran() []string {
	var ran []string
	for _, line := range r.commands {
		if !strings.HasPrefix(line, "dpkg-query ") &&
			!strings.HasPrefix(line, "systemctl list-unit-files ") &&
			!strings.HasPrefix(line, "systemctl show ") {
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
