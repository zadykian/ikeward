package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/install"
)

// installedSystem is a fake system where install ran with installArgs' values, from the profile's
// files in dir, which it returns. It has since forgotten the output and the commands.
func installedSystem(t *testing.T) (*fakeSystem, string) {
	t.Helper()
	dir := writeProfile(t, "")
	f := newFakeSystem(t, "")
	if err := run(installArgs(dir), f.system); err != nil {
		t.Fatal(err)
	}
	f.out.Reset()
	f.ran.commands = nil
	return f, dir
}

// statusOf is the exit status main gives err, 0 for none.
func statusOf(err error) int {
	if err == nil {
		return 0
	}
	return exitStatus(err)
}

// steps are the lines of out that name a step install took, or in a dry run would.
func steps(out string) []string {
	var lines []string
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, "Running ") || strings.HasPrefix(line, "Would run ") {
			lines = append(lines, strings.TrimSuffix(line, "\n"))
		}
	}
	return lines
}

// Run again on a system install took up, install takes only the steps a probe finds due, or every
// step with --all-steps. With --exit-code, it exits 4 where any is due, and 0 where none is.
func TestInstallAgain(t *testing.T) {
	allSteps := []string{"systemctl daemon-reload", "networkctl reload",
		"systemctl enable ikeward-routing.service strongswan.service ikeward-watchdog.timer",
		"systemctl restart ikeward-routing.service", "systemctl start strongswan.service",
		"swanctl --load-all --noprompt", "systemctl start ikeward-watchdog.timer"}
	var every []string
	for _, step := range allSteps {
		every = append(every, "Would run "+step+", as --all-steps asks")
	}
	for _, test := range []struct {
		name   string
		more   []string
		units  map[string]string
		status int
		steps  []string
	}{
		{"a dry run", []string{"--dry-run", "--exit-code"}, nil, 0, nil},
		{"a run", []string{"--exit-code"}, nil, 0, nil},
		{"the timer disabled", []string{"--dry-run", "--exit-code"},
			map[string]string{install.Timer: strings.Replace(healthyUnit, "=enabled", "=disabled", 1)},
			4, []string{"Would run systemctl enable ikeward-routing.service strongswan.service " +
				"ikeward-watchdog.timer, as ikeward-watchdog.timer is disabled"}},
		{"every step", []string{"--all-steps", "--dry-run", "--exit-code"}, nil, 4, every},
		{"every step, without --exit-code", []string{"--all-steps", "--dry-run"}, nil, 0, every},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, dir := installedSystem(t)
			f.ran.units = test.units
			err := run(installArgs(dir, test.more...), f.system)
			if got := statusOf(err); got != test.status {
				t.Errorf("%v: exit status %d, want %d", err, got, test.status)
			}
			if got := steps(f.out.String()); !slices.Equal(got, test.steps) {
				t.Errorf("took the steps %q, want %q; printed:\n%s", got, test.steps, f.out.String())
			}
			if slices.ContainsFunc(f.ran.commands, func(c string) bool {
				return slices.Contains(allSteps, c)
			}) {
				t.Errorf("ran %q", f.ran.commands)
			}
		})
	}
}

// On a new system, install --exit-code exits 4, as does its dry run, and install without it 0. An
// error exits 1 all the same.
func TestInstallExitCode(t *testing.T) {
	for _, test := range []struct {
		more   []string
		uid    int
		status int
		files  []string
	}{
		{[]string{"--exit-code"}, 0, 4, allFiles},
		{nil, 0, 0, allFiles},
		{[]string{"--dry-run", "--exit-code"}, 0, 4, nil},
		{[]string{"--exit-code"}, 1000, 1, nil},
	} {
		f := newFakeSystem(t, "")
		f.uid = test.uid
		err := run(installArgs(writeProfile(t, ""), test.more...), f.system)
		if got := statusOf(err); got != test.status {
			t.Errorf("%q, user %d: %v, exit status %d, want %d", test.more, test.uid, err, got,
				test.status)
		}
		if got := f.installed(t); !slices.Equal(got, test.files) {
			t.Errorf("%q, user %d: wrote %q, want %q", test.more, test.uid, got, test.files)
		}
	}
}
