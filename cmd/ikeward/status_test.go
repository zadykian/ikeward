package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// fakeTunnel is strongSwan with the IKE SAs sas, or one that cannot be asked, with err.
type fakeTunnel struct {
	sas []status.IKESA
	err error
}

func (f fakeTunnel) IKESAs(context.Context, string) ([]status.IKESA, error) { return f.sas, f.err }

// answering is the remote network's DNS server, answering.
type answering struct{}

func (answering) Lookup(context.Context, string, string) error { return nil }

// resolved is what resolvectl prints of ikeward0 with the VPN up.
const resolved = `[{"ifname":"ikeward0","defaultRoute":false,` +
	`"servers":[{"addressString":"192.0.2.10"}],"searchDomains":[` +
	`{"name":"example.test","routeOnly":true}],"scopes":[{"protocol":"dns"}]}]`

// statusRunner prints resolved for resolvectl.
type statusRunner struct{ commands []string }

func (r *statusRunner) Run(name string, args ...string) (string, error) {
	r.commands = append(r.commands, strings.Join(append([]string{name}, args...), " "))
	return resolved, nil
}

// up is strongSwan with the VPN up.
var up = fakeTunnel{sas: []status.IKESA{{State: "ESTABLISHED", VIPs: []string{vip.String()},
	Children: []status.ChildSA{{Name: "ikeward", State: "INSTALLED",
		LocalTS: []string{"203.0.113.130/32"}, RemoteTS: []string{"10.88.0.0/16"}}}}}}

// statusSystem is a system, not as root, whose VPN strongSwan tunnel gives.
func statusSystem(tunnel fakeTunnel, stdout *strings.Builder) system {
	return system{stdout: stdout, tunnel: tunnel, network: newKernel(), resolver: answering{},
		runner: &statusRunner{}, files: memFiles{routing.Learned: "10.88.0.0/16\n",
			files.SettingsFile: settings},
		nat: &fakeNAT{}, euid: func() int { return 1000 }}
}

// The lines of the rule, floor and NAT checks, where routing made them.
const (
	ruled = "ok    rule      32000 looks up main, then 32001 table 220, both with " +
		"suppress_prefixlength 0"
	floored = "ok    floor     table 220 holds unreachable 10.88.0.0/16 metric 4000: while " +
		"the tunnel is down, traffic there goes nowhere; learned: 10.88.0.0/16"
	masqueraded = "ok    NAT       table ip ikeward masquerades what leaves by ikeward0; " +
		"connections so far: 0"
)

// status prints a line per check and exits 0 where all pass, and 1 where any fails.
func TestStatusCommand(t *testing.T) {
	var out strings.Builder
	if err := run([]string{"status"}, statusSystem(up, &out)); err != nil {
		t.Fatal(err)
	}
	want := `ok    IKE SA    ikeward is ESTABLISHED, with 203.0.113.130 from the server
ok    CHILD SA  ikeward is INSTALLED: 203.0.113.130/32 === 10.88.0.0/16
ok    address   203.0.113.130 is on ikeward0
` + ruled + `
ok    route     table 220 routes 10.88.0.0/16 through ikeward0
ok    resolver  192.0.2.10 answers for example.test through the tunnel
ok    DNS       resolved sends ~example.test lookups on ikeward0 to 192.0.2.10
` + floored + `
` + masqueraded + `
`
	if got := out.String(); got != want {
		t.Errorf("printed:\n%s\nwant:\n%s", got, want)
	}

	out.Reset()
	down := fakeTunnel{err: errors.New("/run/charon.vici: permission denied")}
	err := run([]string{"status"}, statusSystem(down, &out))
	if err == nil || err.Error() != "5 of 9 checks failed" || exitStatus(err) != 1 {
		t.Errorf("%v, want 5 of 9 checks failed, and exit status 1", err)
	}
	want = `FAIL  IKE SA    cannot ask strongSwan: /run/charon.vici: permission denied
FAIL  CHILD SA  not known, as strongSwan could not be asked
FAIL  address   no address from the server to look for on ikeward0: strongSwan could not be asked
` + ruled + `
FAIL  route     no range of the server's to look for in table 220: strongSwan could not be asked
FAIL  resolver  not asked, as the tunnel's checks failed
ok    DNS       resolved sends ~example.test lookups on ikeward0 to 192.0.2.10
` + floored + `
` + masqueraded + `
`
	if got := out.String(); got != want {
		t.Errorf("printed:\n%s\nwant:\n%s", got, want)
	}

	// strongSwan's rule, left by a charon that died, fails the rule check, and the resolver goes
	// unasked.
	out.Reset()
	sys := statusSystem(up, &out)
	kernel := newKernel()
	kernel.rules = append(kernel.rules, routing.Rule{Priority: 220, Table: 220,
		SuppressPrefixlen: -1})
	sys.network = kernel
	err = run([]string{"status"}, sys)
	if err == nil || err.Error() != "2 of 9 checks failed" || exitStatus(err) != 1 {
		t.Errorf("%v, want 2 of 9 checks failed, and exit status 1", err)
	}
	rule := "FAIL  rule      220: from all lookup 220 looks up table 220 too, and is not ikeward's\n"
	if got := out.String(); !strings.Contains(got, rule) {
		t.Errorf("printed:\n%s\nwant it to hold:\n%s", got, rule)
	}
}

// Where the NAT table is missing, its check fails alone, and status reads the table only.
func TestStatusNoNAT(t *testing.T) {
	var out strings.Builder
	sys := statusSystem(up, &out)
	nat := &fakeNAT{missing: true}
	sys.nat = nat
	err := run([]string{"status"}, sys)
	if err == nil || err.Error() != "1 of 9 checks failed" || exitStatus(err) != 1 {
		t.Errorf("%v, want 1 of 9 checks failed, and exit status 1", err)
	}
	want := "FAIL  NAT       no table ip ikeward: a forwarded network's traffic leaves ikeward0 " +
		"from its own address, which no SA takes\n"
	if got := out.String(); !strings.Contains(got, want) || strings.Count(got, "FAIL") != 1 {
		t.Errorf("printed:\n%s\nwant it to hold:\n%s", got, want)
	}
	if !slices.Equal(nat.calls, []string{"connections"}) {
		t.Errorf("did %q to the table, want it read alone", nat.calls)
	}
}

// Where status cannot print, it fails, whether the VPN is up or not.
func TestStatusFailingOutput(t *testing.T) {
	for _, tunnel := range []fakeTunnel{up, {}} {
		sys := statusSystem(tunnel, nil)
		sys.stdout = failingWriter{}
		if err := run([]string{"status"}, sys); err == nil || err.Error() != "the pipe is closed" {
			t.Errorf("%v, want the pipe is closed", err)
		}
	}
}
