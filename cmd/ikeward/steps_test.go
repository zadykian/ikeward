package main

import (
	"slices"
	"strings"
	"testing"
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
// step with --all-steps.
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
		name  string
		more  []string
		steps []string
	}{
		{"a dry run", []string{"--dry-run"}, nil},
		{"a run", nil, nil},
		{"every step", []string{"--all-steps", "--dry-run"}, every},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, dir := installedSystem(t)
			if err := run(installArgs(dir, test.more...), f.system); err != nil {
				t.Fatal(err)
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
