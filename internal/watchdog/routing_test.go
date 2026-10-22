package watchdog

import (
	"errors"
	"slices"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// The lines Run prints as it makes the NAT table, the floor and the missing rule again, and once
// every check passes.
const (
	madeNAT   = "Made table ip ikeward: oifname \"ikeward0\" counter masquerade"
	madeFloor = "Made 10.88.0.0/16 unreachable in table 220: unreachable 10.88.0.0/16 " +
		"metric 4000"
	madeRule = "Added rule 32001: from all lookup 220 suppress_prefixlength 0"
	passes   = "Every check passes, with what was missing made again"
)

// Where the rule, floor or NAT check fails alone, Run makes that part again, in ikeward routing's
// order, says so, and checks again. It leaves strongSwan alone: a restart would take the tunnel
// down, and make none of them.
func TestRoutingMade(t *testing.T) {
	for _, test := range []struct {
		name    string
		failed  []status.Result
		changes []string
		made    []string
	}{
		{"NAT", []status.Result{unmasqueraded}, []string{"NAT after 0"}, []string{madeNAT}},
		{"floor", []status.Result{unfloored}, []string{"floor 10.88.0.0/16 after 0"},
			[]string{madeFloor}},
		{"rule", []status.Result{unruled}, []string{"rule 32001 after 0"}, []string{madeRule}},
		{"all", []status.Result{unruled, unfloored, unmasqueraded}, []string{"NAT after 0",
			"floor 10.88.0.0/16 after 0", "rule 32001 after 0"},
			[]string{madeNAT, madeFloor, madeRule}},
	} {
		f := newFakes(with(healthy, test.failed...), healthy)
		if err := f.system().Run(t.Context()); err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		var want []string
		for _, r := range with(healthy, test.failed...) {
			if !r.OK {
				want = append(want, r.String())
			}
		}
		checkLines(t, test.name+": the output", f.lines(), append(append(want, test.made...),
			passes))
		checkLines(t, test.name+": the changes", f.routing.changes, test.changes)
		if len(f.runner.commands) > 0 || len(f.files.writes) > 0 || f.checker.calls != 2 {
			t.Errorf("%s: ran %q, wrote %q and checked %d times", test.name, f.runner.commands,
				f.files.writes, f.checker.calls)
		}
	}

	// The floors are the settings' and one for each range updown learned, as routing.Learned
	// records them.
	f := newFakes(with(healthy, unfloored), healthy)
	f.files.files[routing.Learned] = []byte("192.0.2.0/24\n198.18.0.0/24\n")
	if err := f.system().Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkLines(t, "learned: the changes", f.routing.changes, []string{"floor 10.88.0.0/16 after 0",
		"floor 192.0.2.0/24 after 0", "floor 198.18.0.0/24 after 0"})
	checkLines(t, "learned: the output", f.lines(), []string{unfloored.String(), madeFloor,
		"Made 192.0.2.0/24 unreachable in table 220: unreachable 192.0.2.0/24 metric 4000",
		"Made 198.18.0.0/24 unreachable in table 220: unreachable 198.18.0.0/24 metric 4000",
		passes})

	// While those checks pass, Run makes nothing, whatever else fails.
	for _, runs := range [][][]status.Result{{healthy}, {unhealthy, healthy}} {
		f := newFakes(runs...)
		if err := f.system().Run(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(f.routing.changes) > 0 {
			t.Errorf("made %q", f.routing.changes)
		}
	}
}

// Where the tunnel is down too, Run makes the NAT table first, then restarts strongSwan for the
// checks that still fail.
func TestRoutingAndTunnel(t *testing.T) {
	f := newFakes(with(unhealthy, unmasqueraded), unhealthy, unhealthy, healthy)
	if err := f.system().Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkLines(t, "the output", f.lines(), append(slices.Clone(failedLines),
		unmasqueraded.String(), madeNAT, "Running "+restart,
		"Every check passes 4s after the restart"))
	checkLines(t, "the changes", f.routing.changes, []string{"NAT after 0"})
	checkLines(t, "the commands", f.runner.commands, []string{restart})
}

// Where the kernel refuses a part, Run makes the others, and still restarts strongSwan for the
// checks a restart mends, then fails itself, not as Unhealthy. With the tunnel up, it leaves
// strongSwan alone.
func TestRoutingNotMade(t *testing.T) {
	refused := errors.New("operation not permitted")
	want := "cannot make table ip ikeward: operation not permitted"
	f := newFakes(with(unhealthy, unmasqueraded), with(unhealthy, unmasqueraded),
		with(healthy, unmasqueraded))
	f.routing.err = refused
	err := f.system().Run(t.Context())
	if err == nil || err.Error() != want || errors.As(err, &Unhealthy{}) {
		t.Errorf("%v, want %s, not Unhealthy", err, want)
	}
	checkLines(t, "the output", f.lines(), append(slices.Clone(failedLines),
		unmasqueraded.String(), "Running "+restart, unmasqueraded.String(), "The restart "+
			"mended what it can within 6s: a restart does not change table ip ikeward, which "+
			"the watchdog makes itself"))
	checkLines(t, "the commands", f.runner.commands, []string{restart})

	f = newFakes(with(healthy, unmasqueraded, unfloored))
	f.routing.err = refused
	err = f.system().Run(t.Context())
	if !errors.Is(err, refused) || errors.As(err, &Unhealthy{}) || f.checker.calls != 1 {
		t.Errorf("%v, checked %d times; want both parts' errors, not Unhealthy, checked once",
			err, f.checker.calls)
	}
	checkLines(t, "the changes", f.routing.changes, []string{"NAT after 0",
		"floor 10.88.0.0/16 after 0"})
	checkLines(t, "the output", f.lines(), []string{unfloored.String(), unmasqueraded.String(),
		"Leaving strongSwan as it is: a restart does not change the floors, which the watchdog " +
			"makes itself, nor table ip ikeward, which the watchdog makes itself"})
	if len(f.runner.commands) > 0 {
		t.Errorf("ran %q", f.runner.commands)
	}

	// Where it cannot read the record of the learned ranges, Run makes the settings' floor, then
	// fails itself.
	f = newFakes(with(healthy, unfloored))
	f.files.files[routing.Learned] = []byte("nonsense\n")
	err = f.system().Run(t.Context())
	want = routing.Learned + `: line 1: "nonsense" is no IPv4 range`
	if err == nil || err.Error() != want || errors.As(err, &Unhealthy{}) {
		t.Errorf("%v, want %s, not Unhealthy", err, want)
	}
	checkLines(t, "the record's: the changes", f.routing.changes,
		[]string{"floor 10.88.0.0/16 after 0"})

	// Where the restart fails too, Run returns both errors.
	f = newFakes(with(unhealthy, unmasqueraded))
	f.routing.err, f.runner.err = refused, errFailed
	err = f.system().Run(t.Context())
	if !errors.Is(err, refused) || !errors.Is(err, errFailed) {
		t.Errorf("%v, want the table's error and the restart's", err)
	}
}

// Where it cannot read the settings, Run makes the learned floors, then fails itself.
func TestRoutingWithoutSettings(t *testing.T) {
	f := newFakes(with(healthy, unfloored))
	delete(f.files.files, files.SettingsFile)
	f.files.files[routing.Learned] = []byte("198.18.0.0/24\n")
	err := f.system().Run(t.Context())
	if !errors.Is(err, routing.ErrSettings) || errors.As(err, &Unhealthy{}) {
		t.Errorf("%v, want routing.ErrSettings, not Unhealthy", err)
	}
	checkLines(t, "the changes", f.routing.changes, []string{"floor 198.18.0.0/24 after 0"})
}

// Where the rule check still fails once the rules are made, a restart does not mend it, and Run
// leaves strongSwan alone.
func TestRoutingStillFails(t *testing.T) {
	f := newFakes(with(healthy, unruled))
	err := f.system().Run(t.Context())
	if !errors.As(err, &Unhealthy{}) || err.Error() != "1 of 7 checks failed" {
		t.Errorf("%v, want Unhealthy: 1 of 7 checks failed", err)
	}
	checkLines(t, "the output", f.lines(), []string{unruled.String(), madeRule,
		"Leaving strongSwan as it is: a restart does not change ikeward's rules, which the " +
			"watchdog makes itself"})
	if len(f.runner.commands) > 0 {
		t.Errorf("ran %q", f.runner.commands)
	}
}
