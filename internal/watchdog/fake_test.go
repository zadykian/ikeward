package watchdog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// healthy are the results of a VPN that is up, and unhealthy those of one whose address is gone.
// In both, the checks that a restart does not mend pass, as status.Check gives them.
var (
	healthy = []status.Result{
		{Name: "IKE SA", OK: true, Detail: "ikeward is ESTABLISHED"},
		{Name: "address", OK: true, Detail: "203.0.113.130 is on ikeward0"},
		ruled,
		{Name: "resolver", OK: true, Detail: "192.0.2.10 answers"},
		{Name: status.DNSCheck, OK: true, Detail: "resolved sends ~example.test lookups on ikeward0"},
		floored,
		masqueraded,
	}
	unhealthy = []status.Result{
		{Name: "IKE SA", OK: true, Detail: "ikeward is ESTABLISHED"},
		{Name: "address", Detail: "203.0.113.130 is not on ikeward0"},
		ruled,
		{Name: "resolver", Detail: "not asked"},
		{Name: status.DNSCheck, OK: true, Detail: "resolved sends ~example.test lookups on ikeward0"},
		floored,
		masqueraded,
	}
)

// ruled, floored and masqueraded are the rule, floor and NAT checks' results where ikeward routing
// made them. unruled, unfloored and unmasqueraded are those checks' where they are gone.
var (
	ruled = status.Result{Name: status.RuleCheck, OK: true,
		Detail: "32000 looks up main, then 32001 table 220"}
	floored = status.Result{Name: status.FloorCheck, OK: true,
		Detail: "table 220 holds unreachable 10.88.0.0/16 metric 4000"}
	masqueraded = status.Result{Name: status.NATCheck, OK: true,
		Detail: "table ip ikeward masquerades what leaves by ikeward0"}
	unruled = status.Result{Name: status.RuleCheck,
		Detail: "no rule 32001: from all lookup 220 suppress_prefixlength 0"}
	unfloored = status.Result{Name: status.FloorCheck,
		Detail: "table 220 has no unreachable route to 10.88.0.0/16 at metric 4000"}
	unmasqueraded = status.Result{Name: status.NATCheck, Detail: "no table ip ikeward"}
)

// settings is files.SettingsFile as install writes it, with one floor.
const settings = "dns 192.0.2.10\ndomain example.test\nprobe example.test\n" +
	"floor 10.88.0.0/16\n"

// with is results, where each of failed takes the place of its check's result.
func with(results []status.Result, failed ...status.Result) []status.Result {
	out := slices.Clone(results)
	for _, f := range failed {
		out[slices.IndexFunc(out, func(r status.Result) bool { return r.Name == f.Name })] = f
	}
	return out
}

// failedLines are what Run prints of unhealthy.
var failedLines = []string{
	"FAIL  address   203.0.113.130 is not on ikeward0",
	"FAIL  resolver  not asked",
}

// fakeChecker gives each of runs in turn, and the last once it has given them all. Each run takes
// took on clock.
type fakeChecker struct {
	runs  [][]status.Result
	clock *fakeClock
	took  time.Duration
	calls int
}

func (f *fakeChecker) Check(context.Context) []status.Result {
	results := f.runs[min(f.calls, len(f.runs)-1)]
	f.calls++
	f.clock.now = f.clock.now.Add(f.took)
	return results
}

// fakeFiles is a root directory in memory, which fails where told to.
type fakeFiles struct {
	files               map[string][]byte
	modes               map[string]fs.FileMode
	writes              []string
	failRead, failWrite error
}

func (f *fakeFiles) Read(path string) ([]byte, fs.FileMode, error) {
	if f.failRead != nil {
		return nil, 0, f.failRead
	}
	data, ok := f.files[path]
	if !ok {
		return nil, 0, fs.ErrNotExist
	}
	return slices.Clone(data), f.modes[path], nil
}

func (f *fakeFiles) Write(path string, data []byte, mode fs.FileMode) error {
	if f.failWrite != nil {
		return f.failWrite
	}
	f.writes = append(f.writes, path)
	f.files[path] = slices.Clone(data)
	f.modes[path] = mode
	return nil
}

// fakeRunner runs no command: it records each, and whether files held install.LastRestart as it
// ran, and fails with err.
type fakeRunner struct {
	files    *fakeFiles
	commands []string
	recorded []bool
	err      error
}

func (r *fakeRunner) Run(name string, args ...string) (string, error) {
	r.commands = append(r.commands, strings.Join(append([]string{name}, args...), " "))
	_, ok := r.files.files[install.LastRestart]
	r.recorded = append(r.recorded, ok)
	return "", r.err
}

// fakeRouting is ikeward's routing in the kernel: its NAT table, its floor and its rules, which it
// lists as rules. It records each change, after how many commands runner had run, and fails each
// with err.
type fakeRouting struct {
	runner  *fakeRunner
	rules   []routing.Rule
	changes []string
	err     error
}

func (g *fakeRouting) change(what string) error {
	g.changes = append(g.changes, fmt.Sprintf("%s after %d", what, len(g.runner.commands)))
	return g.err
}

func (g *fakeRouting) Set() error { return g.change("NAT") }

func (g *fakeRouting) SetFloor(dst netip.Prefix) error { return g.change("floor " + dst.String()) }

func (g *fakeRouting) Rules() ([]routing.Rule, error) { return g.rules, nil }

func (g *fakeRouting) AddRule(rule routing.Rule) error {
	return g.change(fmt.Sprintf("rule %d", rule.Priority))
}

func (g *fakeRouting) RemoveRule(rule routing.Rule) error {
	return g.change(fmt.Sprintf("unrule %d", rule.Priority))
}

// fakeClock is a clock whose sleeps pass at once, or fail with err, and which records them.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
	err    error
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.sleeps = append(c.sleeps, d)
	if c.err != nil {
		return c.err
	}
	c.now = c.now.Add(d)
	return nil
}

// start is the fake clock's time as a test starts.
var start = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// fakes are the fakes of a system.
type fakes struct {
	checker *fakeChecker
	routing *fakeRouting
	files   *fakeFiles
	runner  *fakeRunner
	clock   *fakeClock
	out     strings.Builder
}

// newFakes are fakes whose checks give runs in turn.
func newFakes(runs ...[]status.Result) *fakes {
	clock := &fakeClock{now: start}
	disk := &fakeFiles{files: map[string][]byte{files.SettingsFile: []byte(settings)},
		modes: map[string]fs.FileMode{}}
	runner := &fakeRunner{files: disk}
	return &fakes{
		checker: &fakeChecker{runs: runs, clock: clock},
		routing: &fakeRouting{runner: runner, rules: routing.Rules[:1]},
		files:   disk,
		runner:  runner,
		clock:   clock,
	}
}

// system is the system of f.
func (f *fakes) system() System {
	return System{Checker: f.checker, Kernel: f.routing, NAT: f.routing, Files: f.files,
		Runner: f.runner, Clock: f.clock, Out: &f.out}
}

// lines are what Run printed, a line each.
func (f *fakes) lines() []string {
	return strings.Split(strings.TrimSuffix(f.out.String(), "\n"), "\n")
}

// errFailed is the error of a command or a file that fails.
var errFailed = errors.New("failed")

// restart is the command that restarts strongSwan.
const restart = "systemctl restart --no-block strongswan.service"

// checkLines fails t where got's lines are not want.
func checkLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n  %s\nwant:\n  %s", what, strings.Join(got, "\n  "),
			strings.Join(want, "\n  "))
	}
}
