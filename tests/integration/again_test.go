package integration

import (
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
)

// exitChanged is install's exit status under --exit-code where something would change, or did.
const exitChanged = 4

// enableStep is install's step that enables the routing's unit, strongSwan and the watchdog's
// timer.
const enableStep = "systemctl enable " + routing.Unit + " " + install.StrongSwan + " " +
	install.Timer

// takeUp is install's seven take-up steps, in order, as a dry run names them where every step is
// due and neither daemon's settings changed.
var takeUp = []string{
	"systemctl daemon-reload",
	"networkctl reload",
	enableStep,
	"systemctl restart " + routing.Unit,
	"systemctl start " + install.StrongSwan,
	"swanctl --load-all --noprompt",
	"systemctl start " + install.Timer,
}

// installAgain runs install as the install step did, with every check of status passing. A dry run
// plans nothing, and one with --all-steps plans takeUp. With the watchdog's timer disabled by hand,
// a dry run plans enableStep alone, and install runs it alone. Each run takes --exit-code, which
// exits exitChanged where something would change or did, and 0 otherwise.
func (s *stack) installAgain(t *testing.T) {
	s.nothingDue(t, "on a healthy install")

	out, code := s.installExit(t, "--all-steps", "--dry-run", "--exit-code")
	if got := commands(printed(out, "Would ")); code != exitChanged || !slices.Equal(got, takeUp) {
		t.Errorf("install --all-steps --dry-run --exit-code exits %d, not %d, or plans %q, not %q",
			code, exitChanged, got, takeUp)
	}

	s.must(t, "systemctl", "disable", install.Timer)
	why := ", as " + install.Timer + " is disabled"
	out, code = s.installExit(t, "--dry-run", "--exit-code")
	if got, want := printed(out, "Would "), "Would run "+enableStep+why; code != exitChanged ||
		!slices.Equal(got, []string{want}) {
		t.Errorf("with %s disabled, install --dry-run --exit-code exits %d, not %d, or plans %q, "+
			"not %q", install.Timer, code, exitChanged, got, want)
	}
	out, code = s.installExit(t, "--exit-code")
	if got, want := printed(out, "Running "), "Running "+enableStep+why; code != exitChanged ||
		!slices.Equal(got, []string{want}) {
		t.Errorf("with %s disabled, install --exit-code exits %d, not %d, or runs %q, not %q",
			install.Timer, code, exitChanged, got, want)
	}
	// The step leaves the timer as it found it, enabled and started: disable left it running.
	for _, verb := range []string{"is-enabled", "is-active"} {
		s.must(t, "systemctl", verb, "--quiet", install.Timer)
	}
	s.nothingDue(t, "once install enabled "+install.Timer)
}

// nothingDue runs install's dry run with --exit-code, which must plan nothing and exit 0; when
// says when.
func (s *stack) nothingDue(t *testing.T, when string) {
	t.Helper()
	out, code := s.installExit(t, "--dry-run", "--exit-code")
	if got := printed(out, "Would "); code != 0 || len(got) != 0 {
		t.Errorf("%s, install --dry-run --exit-code exits %d, not 0, or plans %q", when, code, got)
	}
}

// printed is the lines of out that start with prefix.
func printed(out, prefix string) []string {
	var got []string
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, prefix) {
			got = append(got, strings.TrimSuffix(line, "\n"))
		}
	}
	return got
}

// commands is the command of each of lines, which a dry run prints as `Would run CMD, as REASON`.
// A line of another form stays whole, and so matches no step.
func commands(lines []string) []string {
	got := make([]string, 0, len(lines))
	for _, line := range lines {
		rest, run := strings.CutPrefix(line, "Would run ")
		command, _, reason := strings.Cut(rest, ", as ")
		if !run || !reason {
			command = line
		}
		got = append(got, command)
	}
	return got
}
