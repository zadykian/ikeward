package status

import (
	"context"
	"errors"
	"io/fs"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// fakeTunnel is strongSwan with the IKE SAs sas, or one that cannot be asked, with err.
type fakeTunnel struct {
	sas []IKESA
	err error
	// asked is the connection asked for, and deadline whether the context had one.
	asked    string
	deadline bool
}

func (f *fakeTunnel) IKESAs(ctx context.Context, conn string) ([]IKESA, error) {
	f.asked = conn
	_, f.deadline = ctx.Deadline()
	return f.sas, f.err
}

// fakeNetwork is a kernel with the addresses addrs, by link, rules, and routes, by table; the
// errors fail each read.
type fakeNetwork struct {
	addrs                         map[string][]netip.Prefix
	rules                         []routing.Rule
	routes                        map[int][]Route
	addrsErr, rulesErr, routesErr error
}

func (f *fakeNetwork) Addrs(link string) ([]netip.Prefix, error) {
	if f.addrsErr != nil {
		return nil, f.addrsErr
	}
	return f.addrs[link], nil
}

func (f *fakeNetwork) Rules() ([]routing.Rule, error) { return f.rules, f.rulesErr }

func (f *fakeNetwork) Routes(table int) ([]Route, error) {
	if f.routesErr != nil {
		return nil, f.routesErr
	}
	return f.routes[table], nil
}

// fakeResolver is a DNS server that answers, or fails with err, and records each question.
type fakeResolver struct {
	err error
	// asked are the server and name of each query, and deadline whether the last had one.
	asked    []string
	deadline bool
}

func (f *fakeResolver) Lookup(ctx context.Context, server, name string) error {
	f.asked = append(f.asked, server+" "+name)
	_, f.deadline = ctx.Deadline()
	return f.err
}

// fakeRunner prints out for resolvectl, or fails with err, and records each command.
type fakeRunner struct {
	out      string
	err      error
	commands []string
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.commands = append(f.commands, strings.Join(append([]string{name}, args...), " "))
	if name != "resolvectl" {
		return "", errors.New("not resolvectl")
	}
	return f.out, f.err
}

// fakeNAT is ikeward's NAT table, whose rule has NATed connections, or which cannot be read as
// ikeward's, with err.
type fakeNAT struct {
	connections uint64
	err         error
}

func (f *fakeNAT) Connections() (uint64, error) { return f.connections, f.err }

// fakeFiles are the files under the root directory, by path; err fails each read of errPath.
type fakeFiles struct {
	files   map[string]string
	errPath string
	err     error
}

func (f *fakeFiles) Read(path string) ([]byte, fs.FileMode, error) {
	data, ok := f.files[path]
	switch {
	case path == f.errPath:
		return nil, 0, f.err
	case !ok:
		return nil, 0, fs.ErrNotExist
	}
	return []byte(data), 0o644, nil
}

// capturedLearned is routing.Learned on a live server, whose VPN server grants 10.88.0.0/16 alone.
const capturedLearned = "# The ranges the VPN server granted.\n10.88.0.0/16\n"

// installedSettings is files.SettingsFile as install writes it on that server.
const installedSettings = "# Written by ikeward install.\ndns 192.0.2.10\ndomain example.test\n" +
	"probe example.test\nfloor 10.88.0.0/16\n"

// testSettings is installedSettings, as routing.ReadSettings reads it.
var testSettings = func() files.Settings {
	s, err := files.ParseSettings([]byte(installedSettings))
	if err != nil {
		panic(err)
	}
	return s
}()

// capturedResolved is what resolvectl --json=short status ikeward0 printed on that server, the VPN
// up, with installedSettings' domain in place of the domains it had then.
const capturedResolved = `[{"ifname":"ikeward0","ifindex":3,"defaultRoute":false,` +
	`"currentServer":{"addressString":"192.0.2.10","address":[192,0,2,10],"family":2,"port":53,` +
	`"ifindex":3,"accessible":true},"servers":[{"addressString":"192.0.2.10",` +
	`"address":[192,0,2,10],"family":2,"port":53,"ifindex":3,"accessible":true}],` +
	`"searchDomains":[{"name":"example.test","routeOnly":true,"ifindex":3}],` +
	`"dnssec":"no","dnsOverTLS":"no","llmnr":"no","mDNS":"no",` +
	`"scopes":[{"protocol":"dns","ifindex":3,"ifname":"ikeward0","dnssec":"no",` +
	`"dnsOverTLS":"no"}]}]` + "\n"

// own is ikeward0's own address, which networkd puts there.
var own = netip.MustParsePrefix("fd22:851f:35dd::1/128")

// fakes are the fakes of a system.
type fakes struct {
	tunnel   *fakeTunnel
	network  *fakeNetwork
	resolver *fakeResolver
	runner   *fakeRunner
	files    *fakeFiles
	nat      *fakeNAT
}

// capturedRules are the rules on that server with ikeward's in place, and the kernel's for the
// local table, main and default.
var capturedRules = []routing.Rule{plain(0, 255), routing.Rules[0], routing.Rules[1],
	plain(32766, 254), plain(32767, 253)}

// plain is a rule at priority that looks up table, with no suppress_prefixlength, as netlink
// lists the kernel's own.
func plain(priority, table int) routing.Rule {
	return routing.Rule{Priority: priority, Table: table, SuppressPrefixlen: -1}
}

// floor is the settings' floor in table 220, which stays whether the tunnel is up or not.
var floor = Route{Dst: netip.MustParsePrefix("10.88.0.0/16"), Type: Unreachable, Metric: 4000}

// through is updown's route through ikeward0 in table 220, as the tunnel is up.
var through = Route{Dst: netip.MustParsePrefix("10.88.0.0/16"), Link: "ikeward0", Type: Unicast}

// healthy are fakes of that server with the VPN up, as strongSwan, netlink and resolvectl showed
// it. The connection's name and the tunnel's link, ikeward0, differ from that server's. ikeward0
// has two IPv6 addresses too: its own, and a link-local one.
func healthy() fakes {
	vip := netip.MustParseAddr("203.0.113.130")
	return fakes{
		tunnel: &fakeTunnel{sas: []IKESA{{
			State: "ESTABLISHED", Remote: "198.51.100.10", VIPs: []string{vip.String()},
			Children: []ChildSA{{Name: "ikeward", State: "INSTALLED",
				LocalTS: []string{"203.0.113.130/32"}, RemoteTS: []string{"10.88.0.0/16"}}},
		}}},
		network: &fakeNetwork{
			addrs: map[string][]netip.Prefix{
				"ikeward0": {netip.PrefixFrom(vip, 32), own, netip.MustParsePrefix("fe80::1/64")},
				"eth0":     {netip.MustParsePrefix("198.51.100.20/32")}},
			rules:  capturedRules,
			routes: map[int][]Route{220: {through, floor}},
		},
		resolver: &fakeResolver{},
		runner:   &fakeRunner{out: capturedResolved},
		files: &fakeFiles{files: map[string]string{routing.Learned: capturedLearned,
			files.SettingsFile: installedSettings}},
		nat: &fakeNAT{},
	}
}

// capturedLines are the lines of that server with the VPN up.
var capturedLines = []string{
	"ok    IKE SA    ikeward is ESTABLISHED, with 203.0.113.130 from the server",
	"ok    CHILD SA  ikeward is INSTALLED: 203.0.113.130/32 === 10.88.0.0/16",
	"ok    address   203.0.113.130 is on ikeward0",
	"ok    rule      32000 looks up main, then 32001 table 220, both with suppress_prefixlength 0",
	"ok    route     table 220 routes 10.88.0.0/16 through ikeward0",
	"ok    resolver  192.0.2.10 answers for example.test through the tunnel",
	"ok    DNS       resolved sends ~example.test lookups on ikeward0 to 192.0.2.10",
	"ok    floor     table 220 holds unreachable 10.88.0.0/16 metric 4000: while the tunnel is " +
		"down, traffic there goes nowhere; learned: 10.88.0.0/16",
	"ok    NAT       " + masquerading + "; connections so far: 0",
}

// masquerading starts the NAT check's line where ikeward's NAT table is in place.
const masquerading = "table ip ikeward masquerades what leaves by ikeward0"

// lines are results as status prints them.
func lines(results []Result) []string {
	var out []string
	for _, r := range results {
		out = append(out, r.String())
	}
	return out
}

// system is the system of f.
func (f fakes) system() System {
	return System{Tunnel: f.tunnel, Network: f.network, Resolver: f.resolver, Runner: f.runner,
		Files: f.files, NAT: f.nat}
}

// change changes the healthy fakes, and the lines it names, by check, differ from capturedLines.
type change struct {
	name  string
	setUp func(f fakes)
	want  map[string]string
}

// notAsked is the resolver check's line where a check of the tunnel failed.
const notAsked = "FAIL  resolver  not asked, as the tunnel's checks failed"

// checkChanges runs the checks on the healthy fakes as each of changes sets them up. It fails t
// where the lines differ from what that names, or Failed counts otherwise.
func checkChanges(t *testing.T, changes []change) {
	t.Helper()
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			f := healthy()
			test.setUp(f)
			results := f.system().Check(t.Context())
			want, failed := expect(results, test.want)
			if got := lines(results); !slices.Equal(got, want) {
				t.Errorf("printed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if got := Failed(results); got != failed {
				t.Errorf("Failed is %d, want %d", got, failed)
			}
		})
	}
}

// expect is capturedLines but for the lines changed names, by the check of results that each is in
// turn, and how many of those lines fail. Where a line before the resolver's fails, the resolver's
// is notAsked, unless changed names it.
func expect(results []Result, changed map[string]string) ([]string, int) {
	var want []string
	failed := 0
	for i, line := range capturedLines {
		name := results[i].Name
		if c, ok := changed[name]; ok {
			line = c
		} else if name == "resolver" && failed > 0 {
			line = notAsked
		}
		if strings.HasPrefix(line, "FAIL") {
			failed++
		}
		want = append(want, line)
	}
	return want, failed
}
