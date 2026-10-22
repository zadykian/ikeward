// Package watchdog is ikeward watchdog, which the watchdog's timer runs every 5 minutes. It runs
// status's checks, and where the rule, floor or NAT check fails, makes that part of ikeward's
// routing again, which a restart of strongSwan does not. Where a check that a restart can mend
// fails, it restarts strongSwan, at most once every 10 minutes. It then checks again until those
// pass or the restart has had its time. A restart mends neither ikeward0's own address nor
// resolved's settings of it. Nor does it change an answer of the remote resolver with an error
// code or no address, which came through the tunnel. It reaches the system through Checker,
// routing.Kernel, routing.NAT, Files, Runner and Clock, which unit tests replace with fakes.
package watchdog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// Checker runs status's checks.
type Checker interface {
	// Check runs the checks, each once, and returns what each found.
	Check(ctx context.Context) []status.Result
}

// Files are the files under the system's root directory, each named by its absolute path. Their
// errors leave out the file's name.
type Files interface {
	// Read returns the file at path and its permission bits; where there is none, an error that
	// wraps fs.ErrNotExist.
	Read(path string) ([]byte, fs.FileMode, error)
	// Write replaces the file at path with data and mode, making its directory where missing.
	Write(path string, data []byte, mode fs.FileMode) error
}

// Runner runs commands.
type Runner interface {
	// Run runs the program name with args and no input, and returns what it printed on stdout.
	Run(name string, args ...string) (string, error)
}

// Clock is the time, and waits.
type Clock interface {
	// Now is the time now.
	Now() time.Time
	// Sleep waits for d, or until ctx ends, and returns ctx's error where it ends first.
	Sleep(ctx context.Context, d time.Duration) error
}

// System is what the watchdog checks and changes.
type System struct {
	Checker Checker
	// Kernel holds ikeward's rules and the floors, and NAT is its NAT table, which the watchdog
	// makes again where their checks fail.
	Kernel routing.Kernel
	NAT    routing.NAT
	// Files hold install.LastRestart, and Runner runs systemctl.
	Files  Files
	Runner Runner
	Clock  Clock
	// Out is where it says what failed, and what it did.
	Out io.Writer
}

const (
	// MinInterval is the least time between two restarts of strongSwan. The timer's next run, 5
	// minutes on, does not repeat a restart that did not help.
	MinInterval = 10 * time.Minute
	// settle is how long after a restart the watchdog checks again, at most, and poll how often.
	// strongSwan connects as it starts, and puts the address and route in place once it has.
	settle = 30 * time.Second
	poll   = 2 * time.Second
)

// printer writes to w, keeping the first error: a failed write stops no repair, and Run returns
// the error at its end.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) printf(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, args...)
	}
}

// failed prints the results that did not pass, as status prints them.
func (p *printer) failed(results []status.Result) {
	for _, r := range results {
		if !r.OK {
			p.printf("%s\n", r)
		}
	}
}

// Unhealthy is Run's error while a check fails. The VPN is down, or ikeward's routing, ikeward0's
// own address or resolved's settings of it are wrong. Or the remote resolver answers with an
// error code or no address. None is a failure of the watchdog's own, which its service tells
// apart by the exit status.
type Unhealthy struct {
	// Failed of Checks checks failed.
	Failed, Checks int
	// After is how long after the restart they failed, where Run restarted strongSwan.
	After time.Duration
}

func (e Unhealthy) Error() string {
	msg := fmt.Sprintf("%d of %d checks failed", e.Failed, e.Checks)
	if e.After > 0 {
		msg += fmt.Sprintf(" %s after the restart", e.After.Round(time.Second))
	}
	return msg
}

// failedChecks is the error of results, of which some failed; after is how long after the
// restart, or 0 where Run did not restart strongSwan.
func failedChecks(results []status.Result, after time.Duration) error {
	return Unhealthy{Failed: status.Failed(results), Checks: len(results), After: after}
}

// Run prints the checks that fail, makes missing routing again, then restarts strongSwan, unless
// it did so under MinInterval ago or a restart mends none. It checks again until all pass, or only
// those a restart does not mend fail twice in a row, for settle at most. It returns nil while all
// pass, else Unhealthy, or an error of its own: the routing, the restart or its record failed.
func (s System) Run(ctx context.Context) error {
	results := s.Checker.Check(ctx)
	if status.Failed(results) == 0 {
		return nil
	}
	out := &printer{w: s.Out}
	out.failed(results)
	// Where the kernel refuses a part, a restart still brings a tunnel that is down up again.
	results, routingErr := s.mendRouting(ctx, out, results)
	err := s.mend(ctx, out, results)
	switch {
	case routingErr == nil:
		return err
	case err == nil || errors.As(err, &Unhealthy{}):
		// The routing's failure is the watchdog's own: as Unhealthy, its service would count it
		// as success.
		return routingErr
	}
	return errors.Join(routingErr, err)
}

// mend restarts strongSwan where a check of results that a restart mends fails, as Run says, and
// returns Run's error.
func (s System) mend(ctx context.Context, out *printer, results []status.Result) error {
	if status.Failed(results) == 0 {
		out.printf("Every check passes, with what was missing made again\n")
		return out.err
	}
	if !mendable(results) {
		out.printf("Leaving strongSwan as it is: a restart does not change %s\n", unmended(results))
		return failedChecks(results, 0)
	}
	since, err := s.sinceRestart(out)
	if err != nil {
		return err
	}
	if since < MinInterval {
		out.printf("Leaving strongSwan as it is: the watchdog restarted it %s ago, and does so "+
			"once in %d minutes at most\n", since.Round(time.Second), MinInterval/time.Minute)
		return failedChecks(results, 0)
	}
	if err := s.restart(out); err != nil {
		return err
	}
	if err := s.recheck(ctx, out); err != nil {
		return err
	}
	return out.err
}

// sinceRestart is how long ago the watchdog last restarted strongSwan, as install.LastRestart
// records it. It gives MinInterval where the record is missing, holds nothing that reads as a
// time, which out notes, or holds a time to come. Such a time, as after the clock was set back,
// would otherwise hold off every restart until the clock caught up.
func (s System) sinceRestart(out *printer) (time.Duration, error) {
	data, _, err := s.Files.Read(install.LastRestart)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return MinInterval, nil
	case err != nil:
		// No restart, which could then repeat on every run of the timer.
		return 0, fmt.Errorf("cannot tell when the watchdog last restarted strongSwan: cannot "+
			"read %s: %w", install.LastRestart, err)
	}
	last, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		out.printf("Note: %s holds no time, so it counts as no restart: %v\n", install.LastRestart,
			err)
		return MinInterval, nil
	}
	since := s.Clock.Now().Sub(last)
	if since < 0 {
		return MinInterval, nil
	}
	return since, nil
}

// restart records the time in install.LastRestart, then restarts strongSwan. Where it cannot
// record the time, it does not restart: every run of the timer could restart strongSwan again.
func (s System) restart(out *printer) error {
	now := s.Clock.Now().UTC().Format(time.RFC3339)
	if err := s.Files.Write(install.LastRestart, []byte(now+"\n"), 0o644); err != nil {
		return fmt.Errorf("cannot record the restart in %s, so strongSwan stays as it is: %w",
			install.LastRestart, err)
	}
	// systemctl does not wait for the restart. Where charon hangs, its stop and its start take
	// 90 s each at most, past the service's TimeoutStartSec, and systemd would end the watchdog
	// before it checks again. recheck waits settle for the restart instead.
	args := []string{"restart", "--no-block", install.StrongSwan}
	out.printf("Running systemctl %s\n", strings.Join(args, " "))
	if _, err := s.Runner.Run("systemctl", args...); err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// recheck runs the checks every poll after the restart until every check passes, and returns
// nil. It stops too once settle has passed, or once only checks that a restart does not mend
// fail twice in a row. It then prints those that still fail and returns their error.
func (s System) recheck(ctx context.Context, out *printer) error {
	restarted := s.Clock.Now()
	unmendedBefore := false
	for {
		if err := s.Clock.Sleep(ctx, poll); err != nil {
			return err
		}
		results := s.Checker.Check(ctx)
		after := s.Clock.Now().Sub(restarted)
		switch {
		case status.Failed(results) == 0:
			out.printf("Every check passes %s after the restart\n", after.Round(time.Second))
			return nil
		case !mendable(results) && unmendedBefore:
			out.failed(results)
			out.printf("The restart mended what it can within %s: a restart does not change %s\n",
				after.Round(time.Second), unmended(results))
			return failedChecks(results, after)
		case after >= settle:
			out.failed(results)
			return failedChecks(results, after)
		}
		unmendedBefore = !mendable(results)
	}
}
