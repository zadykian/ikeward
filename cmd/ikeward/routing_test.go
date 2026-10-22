package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// routingSystem is a system as root, whose kernel is k, with the kernel's own rules alone, and
// whose NAT table is n. updown has learned no range.
func routingSystem(k *fakeKernel, n *fakeNAT, out *strings.Builder) system {
	k.rules = []routing.Rule{{Priority: 0, Table: 255, SuppressPrefixlen: -1},
		{Priority: 32766, Table: routing.Main, SuppressPrefixlen: -1}}
	return system{stdout: out, network: k, nat: n, files: memFiles{files.SettingsFile: settings},
		euid: func() int { return 0 }}
}

// routing makes the NAT table, the floors, then the rules, and says so, as at boot. The floors are
// the settings' and one for each range updown learned.
func TestRoutingCommand(t *testing.T) {
	k, n := &fakeKernel{}, &fakeNAT{}
	var out strings.Builder
	sys := routingSystem(k, n, &out)
	sys.files = memFiles{routing.Learned: "198.18.0.0/24\n", files.SettingsFile: settings}
	if err := run([]string{"routing"}, sys); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(n.calls, []string{"set"}) {
		t.Errorf("did %q to the table, want it made once", n.calls)
	}
	want := []string{"floor 10.88.0.0/16", "floor 198.18.0.0/24",
		"add rule 32000: from all lookup main suppress_prefixlength 0",
		"add rule 32001: from all lookup 220 suppress_prefixlength 0"}
	if !slices.Equal(k.changes, want) {
		t.Errorf("changed %q, want %q", k.changes, want)
	}
	printed := "Made table ip ikeward: oifname \"ikeward0\" counter masquerade\n" +
		"Made 10.88.0.0/16 unreachable in table 220: unreachable 10.88.0.0/16 metric 4000\n" +
		"Made 198.18.0.0/24 unreachable in table 220: unreachable 198.18.0.0/24 metric 4000\n" +
		"Added rule 32000: from all lookup main suppress_prefixlength 0\n" +
		"Added rule 32001: from all lookup 220 suppress_prefixlength 0\n"
	if got := out.String(); got != printed {
		t.Errorf("printed %q, want %q", got, printed)
	}
}

// routing runs as root alone, and exits 1 where the kernel refuses a change or it cannot print.
// A part it made is said, and the parts after one refused are made all the same.
func TestRoutingCommandErrors(t *testing.T) {
	refused := errors.New("operation not permitted")
	made := "Made table ip ikeward: oifname \"ikeward0\" counter masquerade\n"
	floored := "Made 10.88.0.0/16 unreachable in table 220: unreachable 10.88.0.0/16 metric " +
		"4000\n"
	for _, test := range []struct {
		name    string
		setUp   func(*system, *fakeKernel, *fakeNAT)
		want    string
		calls   []string
		printed string
	}{
		{"not root", func(s *system, _ *fakeKernel, _ *fakeNAT) {
			s.euid = func() int { return 1000 }
		}, "routing changes the rules, routes and nftables, and must run as root", nil, ""},
		{"nftables", func(_ *system, _ *fakeKernel, n *fakeNAT) { n.err = refused },
			"cannot make table ip ikeward: operation not permitted", []string{"set"}, floored +
				"Added rule 32000: from all lookup main suppress_prefixlength 0\n" +
				"Added rule 32001: from all lookup 220 suppress_prefixlength 0\n"},
		{"netlink", func(_ *system, k *fakeKernel, _ *fakeNAT) { k.err = refused },
			"cannot make 10.88.0.0/16 unreachable in table 220: operation not permitted\n" +
				"cannot add rule 32000: from all lookup main suppress_prefixlength 0: operation " +
				"not permitted", []string{"set"}, made},
		{"output", func(s *system, _ *fakeKernel, _ *fakeNAT) { s.stdout = failingWriter{} },
			"the pipe is closed\nthe pipe is closed\nthe pipe is closed", []string{"set"}, ""},
	} {
		k, n := &fakeKernel{}, &fakeNAT{}
		var out strings.Builder
		sys := routingSystem(k, n, &out)
		test.setUp(&sys, k, n)
		err := run([]string{"routing"}, sys)
		if err == nil || err.Error() != test.want || exitStatus(err) != 1 {
			t.Errorf("%s: %v, want %s, and exit status 1", test.name, err, test.want)
		}
		if !slices.Equal(n.calls, test.calls) || out.String() != test.printed {
			t.Errorf("%s: did %q to the table, and printed %q; want %q, and %q", test.name,
				n.calls, out.String(), test.calls, test.printed)
		}
	}
}
