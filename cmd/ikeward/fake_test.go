package main

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"io/fs"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/host"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/profile/profiletest"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// fakeTerminal is a terminal on which the user types password, or standard input that is none.
type fakeTerminal struct {
	terminal bool
	password string
	err      error
	reads    int
}

func (f *fakeTerminal) IsTerminal() bool { return f.terminal }

func (f *fakeTerminal) ReadPassword() ([]byte, error) {
	f.reads++
	return []byte(f.password), f.err
}

// fakeRunner runs no command: it records each. dpkg-query reports every package installed, and
// resolvectl prints resolved. systemctl show prints what units holds for a unit it names, and
// healthyUnit for any other.
type fakeRunner struct {
	commands []string
	units    map[string]string
}

// healthyUnit is what systemctl show prints of a unit install took up.
const healthyUnit = "LoadState=loaded\nActiveState=active\nUnitFileState=enabled\n" +
	"NeedDaemonReload=no\n"

// resolved is what resolvectl prints of ikeward0 with the VPN up.
const resolved = `[{"ifname":"ikeward0","defaultRoute":false,` +
	`"servers":[{"addressString":"192.0.2.10"}],"searchDomains":[` +
	`{"name":"example.test","routeOnly":true}],"scopes":[{"protocol":"dns"}]}]`

func (r *fakeRunner) Run(name string, args ...string) (string, error) {
	r.commands = append(r.commands, strings.Join(append([]string{name}, args...), " "))
	switch {
	case name == "dpkg-query":
		return "installed", nil
	case name == "resolvectl":
		return resolved, nil
	case name == "systemctl" && args[0] == "show":
		if out, ok := r.units[args[len(args)-1]]; ok {
			return out, nil
		}
		return healthyUnit, nil
	}
	return "", nil
}

// fakeCharon is strongSwan's charon as swanctl --load-all leaves it. It holds what files has of
// install's files for the profile client: the connection, the client's certificate with its key,
// and the CA's certificate.
type fakeCharon struct{ files install.Files }

func (c fakeCharon) HasConn(_ context.Context, name string) (bool, error) {
	_, _, err := c.files.Read("/etc/swanctl/conf.d/" + name + ".conf")
	return err == nil, nil
}

func (c fakeCharon) Certs(context.Context) ([]install.Cert, error) {
	_, _, noKey := c.files.Read("/etc/swanctl/private/client-key.pem")
	var certs []install.Cert
	for _, f := range []struct {
		path string
		ca   bool
	}{{"/etc/swanctl/x509/client-cert.pem", false}, {"/etc/swanctl/x509ca/" + files.CACert, true}} {
		data, _, err := c.files.Read(f.path)
		if block, _ := pem.Decode(data); err == nil && block != nil {
			certs = append(certs, install.Cert{DER: block.Bytes, CA: f.ca, Key: !f.ca && noKey == nil})
		}
	}
	return certs, nil
}

// fakeNAT is ikeward's NAT table, missing where missing says, beside other tables that tables
// names. It records what each command does to it, and fails each call with err.
type fakeNAT struct {
	calls   []string
	missing bool
	tables  map[string]bool
	err     error
}

func (f *fakeNAT) Set() error {
	f.calls = append(f.calls, "set")
	f.missing = f.missing && f.err != nil
	return f.err
}

func (f *fakeNAT) HasTable(name string) (bool, error) {
	return (name == routing.NATTable && !f.missing) || f.tables[name], f.err
}

func (f *fakeNAT) RemoveTable(name string) error {
	f.calls = append(f.calls, "remove "+name)
	return f.err
}

func (f *fakeNAT) Connections() (uint64, error) {
	f.calls = append(f.calls, "connections")
	if f.missing {
		return 0, routing.Missing{Err: errors.New("no table ip ikeward")}
	}
	return 0, f.err
}

// memFiles are files in memory, by path, as a root directory holds them.
type memFiles map[string]string

func (m memFiles) Read(path string) ([]byte, fs.FileMode, error) {
	data, ok := m[path]
	if !ok {
		return nil, 0, fs.ErrNotExist
	}
	return []byte(data), 0o644, nil
}

func (m memFiles) Write(path string, data []byte, _ fs.FileMode) error {
	m[path] = string(data)
	return nil
}

func (m memFiles) Remove(path string) error {
	if _, ok := m[path]; !ok {
		return fs.ErrNotExist
	}
	delete(m, path)
	return nil
}

// fakeKernel is a kernel with ikeward's rules, and table 220's route and floor, unless rules or
// routes say otherwise. It has the link ikeward0 alone, with the VPN's address and its own. It
// records each change, which it makes, and fails each with err.
type fakeKernel struct {
	rules   []routing.Rule
	routes  []status.Route
	changes []string
	err     error
}

// newKernel is a fakeKernel as ikeward routing and updown leave it, the tunnel up.
func newKernel() *fakeKernel {
	subnet := netip.MustParsePrefix("10.88.0.0/16")
	return &fakeKernel{
		rules: append([]routing.Rule{{Priority: 0, Table: 255, SuppressPrefixlen: -1}},
			routing.Rules...),
		routes: []status.Route{{Dst: subnet, Link: files.Link, Type: status.Unicast},
			{Dst: subnet, Type: status.Unreachable, Metric: routing.FloorMetric}},
	}
}

// vip is the address the fake server assigns.
var vip = netip.MustParseAddr("203.0.113.130")

func (*fakeKernel) Addrs(string) ([]netip.Prefix, error) {
	return []netip.Prefix{netip.PrefixFrom(vip, 32), netip.MustParsePrefix(files.LinkULA)}, nil
}

func (k *fakeKernel) Rules() ([]routing.Rule, error) { return slices.Clone(k.rules), nil }

func (k *fakeKernel) Routes(table int) ([]status.Route, error) {
	if table != files.Table {
		return nil, nil
	}
	return k.routes, nil
}

func (*fakeKernel) HasLink(name string) (bool, error) { return name == files.Link, nil }

func (k *fakeKernel) change(what string) error {
	k.changes = append(k.changes, what)
	return k.err
}

func (k *fakeKernel) RemoveLink(name string) error { return k.change("remove link " + name) }

func (k *fakeKernel) AddRule(rule routing.Rule) error {
	if err := k.change("add rule " + rule.String()); err != nil {
		return err
	}
	k.rules = append(k.rules, rule)
	return nil
}

func (k *fakeKernel) RemoveRule(rule routing.Rule) error {
	if err := k.change("remove rule " + rule.String()); err != nil {
		return err
	}
	k.rules = slices.DeleteFunc(k.rules, func(r routing.Rule) bool { return r == rule })
	return nil
}

func (k *fakeKernel) SetFloor(dst netip.Prefix) error { return k.change("floor " + dst.String()) }

func (k *fakeKernel) RemoveRoutes(int) (int, error) {
	n := len(k.routes)
	if err := k.change("remove routes"); err != nil {
		return 0, err
	}
	k.routes = nil
	return n, nil
}

// fakeNames is the system's resolver. It gives profiletest.Server, the VPN server, the address
// 198.51.100.10, unless down, and fails for any other name. It records each name asked.
type fakeNames struct {
	asked []string
	down  bool
}

func (f *fakeNames) IPv4(_ context.Context, name string) ([]netip.Addr, error) {
	f.asked = append(f.asked, name)
	if name != profiletest.Server || f.down {
		return nil, errors.New("lookup " + name + ": no such host")
	}
	return []netip.Addr{netip.MustParseAddr("198.51.100.10")}, nil
}

// fakeSystem is a system as root, whose files are under a temporary directory, root, and whose
// standard input, not a terminal, holds stdin. ikeward runs as program, files.Binary unless set.
type fakeSystem struct {
	system
	root        string
	out, errOut bytes.Buffer
	term        fakeTerminal
	ran         fakeRunner
	nat         fakeNAT
	kernel      *fakeKernel
	names       fakeNames
	uid         int
	program     string
}

func newFakeSystem(t *testing.T, stdin string) *fakeSystem {
	t.Helper()
	f := &fakeSystem{root: t.TempDir(), kernel: newKernel()}
	f.system = system{
		stdin: strings.NewReader(stdin), stdout: &f.out, stderr: &f.errOut,
		terminal: &f.term, files: host.Root(f.root), runner: &f.ran, nat: &f.nat,
		charon:  fakeCharon{host.Root(f.root)},
		network: f.kernel, names: &f.names, euid: func() int { return f.uid },
		runsAs: func(path string) (string, bool, error) {
			if f.program == "" {
				return path, true, nil
			}
			return f.program, f.program == path, nil
		},
	}
	return f
}

// settings is files.SettingsFile as install writes it with installArgs' values.
const settings = "dns 192.0.2.10\ndomain example.test\nprobe example.test\n" +
	"floor 10.88.0.0/16\n"

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("the pipe is closed") }

// failAfter fails every write after the first.
type failAfter struct{ written bool }

func (f *failAfter) Write(p []byte) (int, error) {
	if f.written {
		return 0, errors.New("the pipe is closed")
	}
	f.written = true
	return len(p), nil
}
