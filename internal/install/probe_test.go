package install

import (
	"bytes"
	"errors"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// fakes are the fakes of a system that install set up, for a probe to find otherwise.
type fakes struct {
	runner  *fakeRunner
	network *fakeNetwork
	tables  *fakeTables
	charon  *fakeCharon
}

// probeCase is a probe that finds a system that install set up otherwise, as setUp leaves it.
// want are the steps due, each its command, then why.
type probeCase struct {
	name  string
	setUp func(fakes)
	want  []string
}

// checkProbes runs a dry run of install for each of cases, which must say it would change the
// system, and take the steps want names alone.
func checkProbes(t *testing.T, cases []probeCase) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			p := newProfile(t, "client")
			f, r := newFakeFiles(), newFakeRunner(Packages...)
			installOn(t, f, r, p)
			var out bytes.Buffer
			s := dryRunOn(f, r, &out)
			fs := fakes{r, s.Network.(*fakeNetwork), s.Tables.(*fakeTables), s.Charon.(*fakeCharon)}
			test.setUp(fs)
			changed, err := s.Install(t.Context(), p, settings)
			if err != nil || !changed {
				t.Fatalf("Install: %v, %v", changed, err)
			}
			wantOut := []string{"Left 12 of the 12 files as they were"}
			for _, line := range test.want {
				wantOut = append(wantOut, "Would run "+line)
			}
			checkLines(t, "the output", outputLines(t, out.String()), wantOut)
		})
	}
}

// What systemctl show says of a unit makes its steps due: systemd reloads one changed on disk or
// not loaded, enables one not enabled, and starts one not active. systemctl failing does too.
func TestUnitProbes(t *testing.T) {
	routingUnit := "systemctl restart " + routing.Unit
	failed := errors.New("exit status 1")
	checkProbes(t, []probeCase{
		{"changed on disk", func(f fakes) { f.runner.states[Service+" NeedDaemonReload"] = "yes" },
			[]string{"systemctl daemon-reload, as ikeward-watchdog.service changed on disk"}},
		{"not loaded", func(f fakes) { f.runner.states[routing.Unit+" LoadState"] = "not-found" },
			[]string{"systemctl daemon-reload, as ikeward-routing.service is not-found"}},
		{"disabled", func(f fakes) { f.runner.states[StrongSwan+" UnitFileState"] = "disabled" },
			[]string{enable + ", as strongswan.service is disabled"}},
		{"no unit file", func(f fakes) { f.runner.states[Timer+" UnitFileState"] = "" },
			[]string{enable + ", as ikeward-watchdog.timer is not enabled"}},
		{"routing failed", func(f fakes) { f.runner.states[routing.Unit+" ActiveState"] = "failed" },
			[]string{routingUnit + ", as ikeward-routing.service is failed"}},
		{"strongSwan stopped", func(f fakes) {
			f.runner.states[StrongSwan+" ActiveState"] = "inactive"
		}, []string{"systemctl start strongswan.service, as strongswan.service is inactive"}},
		{"timer stopped", func(f fakes) { f.runner.states[Timer+" ActiveState"] = "inactive" },
			[]string{"systemctl start ikeward-watchdog.timer, as ikeward-watchdog.timer is inactive"}},
		{"systemctl fails", func(f fakes) {
			f.runner.fail["systemctl show --property=LoadState,ActiveState,UnitFileState,"+
				"NeedDaemonReload "+Timer] = failed
		}, []string{
			"systemctl daemon-reload, as systemctl show ikeward-watchdog.timer fails: exit status 1",
			enable + ", as systemctl show ikeward-watchdog.timer fails: exit status 1",
			"systemctl start ikeward-watchdog.timer, as systemctl show ikeward-watchdog.timer " +
				"fails: exit status 1",
		}},
	})
}

// files.Link missing, or its own address, or a failing DNS check make networkd reload. A failing
// rule, floor or NAT check restarts the routing's unit.
func TestNetworkProbes(t *testing.T) {
	routingUnit := "systemctl restart " + routing.Unit
	failed := errors.New("exit status 1")
	checkProbes(t, []probeCase{
		{"no link", func(f fakes) { f.network.links = nil },
			[]string{"networkctl reload, as there is no link ikeward0"}},
		{"links unread", func(f fakes) { f.network.linkErr = failed },
			[]string{"networkctl reload, as the kernel cannot say whether there is a link " +
				"ikeward0: exit status 1"}},
		{"no own address", func(f fakes) { f.network.addrs = nil },
			[]string{"networkctl reload, as ikeward0 lacks its own address, " + files.LinkULA}},
		{"DNS", func(f fakes) { f.runner.resolved = "[]" }, []string{
			"networkctl reload, as the DNS check fails: resolvectl says nothing of ikeward0"}},
		{"rule", func(f fakes) { f.network.rules = f.network.rules[:2] }, []string{
			routingUnit + ", as the rule check fails: no rule 32001: from all lookup 220 " +
				"suppress_prefixlength 0"}},
		{"floor", func(f fakes) { f.network.routes[files.Table] = f.network.routes[files.Table][:1] },
			[]string{routingUnit + ", as the floor check fails: table 220 has no unreachable " +
				"route to 10.88.0.0/16 at metric 4000: while the tunnel is down, traffic there " +
				"takes the default route; learned: none yet"}},
		{"NAT", func(f fakes) { f.tables.tables = nil }, []string{
			routingUnit + ", as the NAT check fails: there is no table ip ikeward: a forwarded " +
				"network's traffic leaves ikeward0 from its own address, which no SA takes"}},
	})
}

// charon lacking the connection, the profile's certificate or key, or the CA has swanctl load the
// files again, as charon failing to answer does.
func TestCharonProbes(t *testing.T) {
	load := "swanctl --load-all --noprompt"
	checkProbes(t, []probeCase{
		{"strongSwan unasked", func(f fakes) { f.charon.err = errors.New("/run/charon.vici: gone") },
			[]string{load + ", as strongSwan cannot be asked: /run/charon.vici: gone"}},
		{"no connection", func(f fakes) { f.charon.noConn = true },
			[]string{load + ", as strongSwan has not loaded the connection ikeward"}},
		{"no certificate", func(f fakes) { f.charon.noCert = true },
			[]string{load + ", as strongSwan has not loaded the profile's certificate"}},
		{"no key", func(f fakes) { f.charon.noKey = true },
			[]string{load + ", as strongSwan has not loaded the profile's key"}},
		{"no CA", func(f fakes) { f.charon.noCA = true },
			[]string{load + ", as strongSwan has not loaded the CA's certificate"}},
	})
}
