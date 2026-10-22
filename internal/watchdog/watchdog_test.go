package watchdog

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/status"
)

// While every check passes, Run prints nothing, and changes nothing.
func TestHealthy(t *testing.T) {
	f := newFakes(healthy)
	if err := f.system().Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.out.Len() > 0 || len(f.runner.commands) > 0 || len(f.files.writes) > 0 ||
		len(f.clock.sleeps) > 0 {
		t.Errorf("printed %q, ran %q, wrote %q and slept %v", f.out.String(), f.runner.commands,
			f.files.writes, f.clock.sleeps)
	}
	if f.checker.calls != 1 {
		t.Errorf("checked %d times, want once", f.checker.calls)
	}
}

// Where a check fails, Run records the time, then restarts strongSwan, and checks again every 2
// seconds until every check passes.
func TestRepaired(t *testing.T) {
	f := newFakes(unhealthy, unhealthy, healthy)
	if err := f.system().Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkLines(t, "the output", f.lines(), append(slices.Clone(failedLines),
		"Running "+restart, "Every check passes 4s after the restart"))
	checkLines(t, "the commands", f.runner.commands, []string{restart})
	if !slices.Equal(f.runner.recorded, []bool{true}) {
		t.Error("restarted strongSwan before it recorded the time")
	}
	if got, want := string(f.files.files[install.LastRestart]), "2026-10-05T12:00:00Z\n"; got != want {
		t.Errorf("%s holds %q, want %q", install.LastRestart, got, want)
	}
	if got := f.files.modes[install.LastRestart]; got != 0o644 {
		t.Errorf("%s's mode is %04o, want 0644", install.LastRestart, got)
	}
	if want := []time.Duration{2 * time.Second, 2 * time.Second}; !slices.Equal(f.clock.sleeps, want) {
		t.Errorf("slept %v, want %v", f.clock.sleeps, want)
	}
}

// Where the checks still fail 30 seconds after the restart, Run prints those that fail, and
// returns their error.
func TestNotRepaired(t *testing.T) {
	f := newFakes(unhealthy)
	f.checker.took = time.Second
	err := f.system().Run(t.Context())
	if !errors.As(err, &Unhealthy{}) || err.Error() != "2 of 7 checks failed 30s after the restart" {
		t.Errorf("%v, want Unhealthy: 2 of 7 checks failed 30s after the restart", err)
	}
	want := append(slices.Clone(failedLines), "Running "+restart)
	checkLines(t, "the output", f.lines(), append(want, failedLines...))
	checkLines(t, "the commands", f.runner.commands, []string{restart})
	// Each of 10 rounds sleeps 2 seconds and checks for 1, after the first check.
	if f.checker.calls != 11 || len(f.clock.sleeps) != 10 {
		t.Errorf("checked %d times and slept %d, want 11 and 10", f.checker.calls,
			len(f.clock.sleeps))
	}
}

// Run leaves strongSwan alone where it restarted it under 10 minutes ago, as
// install.LastRestart records.
func TestLimit(t *testing.T) {
	for _, test := range []struct{ last, ago string }{
		{"2026-10-05T11:56:00Z\n", "4m0s"},
		{"2026-10-05T12:50:01+01:00", "9m59s"},
	} {
		f := newFakes(unhealthy, healthy)
		f.files.files[install.LastRestart] = []byte(test.last)
		err := f.system().Run(t.Context())
		if !errors.As(err, &Unhealthy{}) || err.Error() != "2 of 7 checks failed" {
			t.Errorf("%v, want Unhealthy: 2 of 7 checks failed", err)
		}
		checkLines(t, "the output", f.lines(), append(slices.Clone(failedLines),
			"Leaving strongSwan as it is: the watchdog restarted it "+test.ago+" ago, and does "+
				"so once in 10 minutes at most"))
		if len(f.runner.commands) > 0 || len(f.files.writes) > 0 {
			t.Errorf("ran %q and wrote %q", f.runner.commands, f.files.writes)
		}
	}
}

// Run restarts strongSwan where install.LastRestart records no restart under 10 minutes ago. A
// time to come, as after the clock was set back, or one that does not read, counts as none.
func TestNoLimit(t *testing.T) {
	for _, test := range []struct{ name, last, note string }{
		{"none", "", ""},
		{"10 minutes ago", "2026-10-05T11:50:00Z\n", ""},
		{"yesterday", "2026-10-04T12:00:00Z\n", ""},
		{"to come", "2026-10-05T13:00:00Z\n", ""},
		{"no time", "yesterday\n", "Note: " + install.LastRestart + ` holds no time, so it counts ` +
			`as no restart: parsing time "yesterday" as "2006-01-02T15:04:05Z07:00": cannot parse ` +
			`"yesterday" as "2006"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFakes(unhealthy, healthy)
			if test.last != "" {
				f.files.files[install.LastRestart] = []byte(test.last)
			}
			if err := f.system().Run(t.Context()); err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(failedLines)
			if test.note != "" {
				want = append(want, test.note)
			}
			checkLines(t, "the output", f.lines(), append(want, "Running "+restart,
				"Every check passes 2s after the restart"))
			checkLines(t, "the commands", f.runner.commands, []string{restart})
		})
	}
}

func TestErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		setUp    func(*fakes)
		want     string
		restarts bool
	}{
		{"record unread", func(f *fakes) { f.files.failRead = fs.ErrPermission },
			"cannot tell when the watchdog last restarted strongSwan: cannot read " +
				install.LastRestart + ": permission denied", false},
		{"record unwritten", func(f *fakes) { f.files.failWrite = errFailed },
			"cannot record the restart in " + install.LastRestart +
				", so strongSwan stays as it is: failed", false},
		{"restart", func(f *fakes) { f.runner.err = errFailed }, restart + ": failed", true},
		{"cancelled", func(f *fakes) { f.clock.err = context.Canceled }, "context canceled", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFakes(unhealthy, healthy)
			test.setUp(f)
			err := f.system().Run(t.Context())
			if err == nil || err.Error() != test.want || errors.As(err, &Unhealthy{}) {
				t.Errorf("%v, want %s, not Unhealthy", err, test.want)
			}
			if got := len(f.runner.commands) > 0; got != test.restarts {
				t.Errorf("ran %q", f.runner.commands)
			}
		})
	}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("the pipe is closed") }

// A failed write of the output stops no repair, and fails Run once Run is done. While the checks
// fail, their error is Run's.
func TestOutputFails(t *testing.T) {
	for _, test := range []struct {
		runs [][]status.Result
		want string
	}{
		{[][]status.Result{unhealthy, healthy}, "the pipe is closed"},
		{[][]status.Result{unhealthy}, "2 of 7 checks failed 30s after the restart"},
	} {
		f := newFakes(test.runs...)
		s := f.system()
		s.Out = failingWriter{}
		if err := s.Run(t.Context()); err == nil || err.Error() != test.want {
			t.Errorf("%v, want %s", err, test.want)
		}
		checkLines(t, "the commands", f.runner.commands, []string{restart})
	}
}
