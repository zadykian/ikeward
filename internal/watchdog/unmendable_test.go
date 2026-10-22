package watchdog

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// Where only status.DNSCheck fails, Run leaves strongSwan alone, which a restart would take
// down and not mend, and returns Unhealthy all the same. Its note names what networkd gives
// ikeward0 alone, as routing's checks pass.
func TestDNSOnly(t *testing.T) {
	results := with(healthy,
		status.Result{Name: status.DNSCheck, Detail: "ikeward0: it has no domain ~example.com"})
	f := newFakes(results, healthy)
	err := f.system().Run(t.Context())
	if !errors.As(err, &Unhealthy{}) || err.Error() != "1 of 7 checks failed" {
		t.Errorf("%v, want Unhealthy: 1 of 7 checks failed", err)
	}
	checkLines(t, "the output", f.lines(), []string{
		"FAIL  DNS       ikeward0: it has no domain ~example.com",
		"Leaving strongSwan as it is: a restart does not change ikeward0's own address and " +
			"resolved's settings, which networkd gives",
	})
	if len(f.runner.commands) > 0 || len(f.files.writes) > 0 || f.checker.calls != 1 {
		t.Errorf("ran %q, wrote %q and checked %d times", f.runner.commands, f.files.writes,
			f.checker.calls)
	}

	// With another check failing, it restarts strongSwan.
	results[1] = unhealthy[1]
	f = newFakes(results, healthy)
	if err := f.system().Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkLines(t, "the commands", f.runner.commands, []string{restart})
}

// Where only routing's checks fail with status.DNSCheck, once their parts are made, Run leaves
// strongSwan alone: a restart makes none of them. Its note names what the checks that failed
// read, in their order, and nothing of those that pass.
func TestRoutingOnly(t *testing.T) {
	dns := status.Result{Name: status.DNSCheck, Detail: "ikeward0: it has no domain ~example.com"}
	for _, test := range []struct {
		failed []status.Result
		why    string
	}{
		{[]status.Result{unruled}, "ikeward's rules, which the watchdog makes itself"},
		{[]status.Result{dns, unfloored}, "ikeward0's own address and resolved's settings, which " +
			"networkd gives, nor the floors, which the watchdog makes itself"},
	} {
		results := with(healthy, test.failed...)
		f := newFakes(results)
		err := f.system().Run(t.Context())
		want := fmt.Sprintf("%d of %d checks failed", len(test.failed), len(results))
		if !errors.As(err, &Unhealthy{}) || err.Error() != want {
			t.Errorf("%v, want Unhealthy: %s", err, want)
		}
		if len(f.runner.commands) > 0 || len(f.files.writes) > 0 || f.checker.calls != 2 {
			t.Errorf("ran %q, wrote %q and checked %d times", f.runner.commands, f.files.writes,
				f.checker.calls)
		}
		lines := f.lines()
		checkLines(t, "the note", lines[len(lines)-1:],
			[]string{"Leaving strongSwan as it is: a restart does not change " + test.why})
	}

	// With another check failing, it restarts strongSwan, and stops checking once only the
	// floor fails twice in a row.
	f := newFakes(with(unhealthy, unfloored), with(unhealthy, unfloored), with(healthy, unfloored))
	err := f.system().Run(t.Context())
	if want := "1 of 7 checks failed 4s after the restart"; !errors.As(err, &Unhealthy{}) ||
		err.Error() != want {
		t.Errorf("%v, want Unhealthy: %s", err, want)
	}
	checkLines(t, "the output", f.lines(), append(slices.Clone(failedLines), unfloored.String(),
		madeFloor, "Running "+restart, unfloored.String(), "The restart mended what it can "+
			"within 4s: a restart does not change the floors, which the watchdog makes itself"))
	checkLines(t, "the commands", f.runner.commands, []string{restart})
}

// Where DNS alone fails once after the restart, Run checks again, and stops once every check
// passes. Only twice in a row does such a failure end its checks.
func TestDNSFailsOnce(t *testing.T) {
	dns := status.Result{Name: status.DNSCheck, Detail: "ikeward0: resolved sends it no lookup"}
	f := newFakes(with(unhealthy, dns), with(healthy, dns), healthy)
	if err := f.system().Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkLines(t, "the output", f.lines(), append(slices.Clone(failedLines), dns.String(),
		"Running "+restart, "Every check passes 4s after the restart"))
	checkLines(t, "the commands", f.runner.commands, []string{restart})
}

// answer is the resolver check's result where the remote resolver answers SERVFAIL through
// the tunnel, and leaving is Run's note of it.
var (
	answer = status.Result{Name: "resolver", Err: fmt.Errorf("%w SERVFAIL", status.ErrAnswer),
		Detail: "192.0.2.10 gives no address for example.test: it answers SERVFAIL through " +
			"the tunnel"}
	leaving = "Leaving strongSwan as it is: a restart does not change the remote resolver's " +
		"answer, which came through the tunnel"
)

// Where the remote resolver answers with an error, which a restart does not change, Run
// leaves strongSwan alone, and returns Unhealthy all the same. Where it gives no answer, Run
// restarts strongSwan, as the tunnel may carry nothing.
func TestResolverAnswers(t *testing.T) {
	dns := status.Result{Name: status.DNSCheck, Detail: "ikeward0: it has no domain ~example.com"}
	for _, test := range []struct {
		failed []status.Result
		note   string
	}{
		{[]status.Result{answer}, leaving},
		{[]status.Result{answer, dns}, leaving + ", nor ikeward0's own address and resolved's " +
			"settings, which networkd gives"},
	} {
		f := newFakes(with(healthy, test.failed...), healthy)
		err := f.system().Run(t.Context())
		want := fmt.Sprintf("%d of 7 checks failed", len(test.failed))
		if !errors.As(err, &Unhealthy{}) || err.Error() != want {
			t.Errorf("%v, want Unhealthy: %s", err, want)
		}
		var lines []string
		for _, r := range test.failed {
			lines = append(lines, r.String())
		}
		checkLines(t, "the output", f.lines(), append(lines, test.note))
		if len(f.runner.commands) > 0 || len(f.files.writes) > 0 || f.checker.calls != 1 {
			t.Errorf("ran %q, wrote %q and checked %d times", f.runner.commands, f.files.writes,
				f.checker.calls)
		}
	}

	silent := status.Result{Name: "resolver", Err: errors.New("no answer in time"),
		Detail: "192.0.2.10 gives no address for example.test: no answer in time"}
	f := newFakes(with(healthy, silent), healthy)
	if err := f.system().Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkLines(t, "the output", f.lines(), []string{silent.String(), "Running " + restart,
		"Every check passes 2s after the restart"})
	checkLines(t, "the commands", f.runner.commands, []string{restart})
}

// Where the resolver answers with an error twice in a row after a restart, the tunnel is up, and
// Run stops checking, as a restart does not change the answer.
func TestResolverAnswersAfterRestart(t *testing.T) {
	f := newFakes(unhealthy, with(healthy, answer), with(healthy, answer), healthy)
	err := f.system().Run(t.Context())
	if want := "1 of 7 checks failed 4s after the restart"; !errors.As(err, &Unhealthy{}) ||
		err.Error() != want {
		t.Errorf("%v, want Unhealthy: %s", err, want)
	}
	checkLines(t, "the output", f.lines(), append(slices.Clone(failedLines), "Running "+restart,
		answer.String(), "The restart mended what it can within 4s: a restart does not change "+
			"the remote resolver's answer, which came through the tunnel"))
	checkLines(t, "the commands", f.runner.commands, []string{restart})
	if f.checker.calls != 3 {
		t.Errorf("checked %d times, want 3", f.checker.calls)
	}
}

// The checks that need the settings fail with routing.ErrSettings where those cannot be read. Run
// leaves strongSwan alone, which a restart would take down and not mend, and names the file once.
func TestSettingsUnread(t *testing.T) {
	err := fmt.Errorf("%w, which install writes: file does not exist", routing.ErrSettings)
	unread := func(name string) status.Result {
		return status.Result{Name: name, Detail: err.Error(), Err: err}
	}
	f := newFakes(with(healthy, unread("resolver"), unread(status.DNSCheck)))
	if err := f.system().Run(t.Context()); !errors.As(err, &Unhealthy{}) ||
		err.Error() != "2 of 7 checks failed" {
		t.Errorf("%v, want Unhealthy: 2 of 7 checks failed", err)
	}
	lines := f.lines()
	checkLines(t, "the note", lines[len(lines)-1:], []string{"Leaving strongSwan as it is: a " +
		"restart does not change " + files.SettingsFile + ", which install writes"})
	if len(f.runner.commands) > 0 || len(f.files.writes) > 0 {
		t.Errorf("ran %q, and wrote %q", f.runner.commands, f.files.writes)
	}
}
