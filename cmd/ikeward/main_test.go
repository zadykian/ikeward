package main

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	for _, option := range []string{"--version", "-v"} {
		var out bytes.Buffer
		if err := run([]string{option}, system{stdout: &out}); err != nil {
			t.Fatalf("%s: %v", option, err)
		}
		if got, want := out.String(), "ikeward version dev\n"; got != want {
			t.Errorf("%s printed %q, want %q", option, got, want)
		}
	}
}

// TestBinary builds ikeward, since install.sh will rely on the version that -X main.version
// stamps in, and only main prints an error and exits with its status.
func TestBinary(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikeward")
	build := exec.Command("go", "build", "-o", binary, "-ldflags", "-X main.version=1.2.3", ".")
	build.Env = append(build.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}

	output, err := exec.Command(binary, "--version").Output()
	if err != nil {
		t.Fatalf("ikeward --version: %v", err)
	}
	if got, want := string(output), "ikeward version 1.2.3\n"; got != want {
		t.Errorf("ikeward --version printed %q, want %q", got, want)
	}

	var stdout, stderr bytes.Buffer
	nope := exec.Command(binary, "--nope")
	nope.Stdout, nope.Stderr = &stdout, &stderr
	var exit *exec.ExitError
	if err := nope.Run(); !errors.As(err, &exit) {
		t.Fatalf("ikeward --nope: %v, want an exit status", err)
	}
	if got := exit.ExitCode(); got != 2 {
		t.Errorf("ikeward --nope: exit status %d, want 2", got)
	}
	if stdout.Len() > 0 {
		t.Errorf("ikeward --nope printed %q to stdout", stdout.String())
	}
	want := "ikeward: unknown flag: --nope (see ikeward --help)\n"
	if got := stderr.String(); got != want {
		t.Errorf("ikeward --nope printed %q to stderr, want %q", got, want)
	}
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"-h"}, {"help"}} {
		var out bytes.Buffer
		if err := run(args, system{stdout: &out}); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		help := out.String()
		if !strings.HasPrefix(help, rootLong+"\n") {
			t.Errorf("%q: the help does not start with the root's text:\n%s", args, help)
		}
		for _, want := range []string{"\n  ikeward [flags]\n", "\n  -v, --version ",
			"\n  install     ", "\n  routing     ", "\n  status      ", "\n  uninstall   ",
			"\n  updown      ", "\n  watchdog    "} {
			if !strings.Contains(help, want) {
				t.Errorf("%q: the help has no %q:\n%s", args, want, help)
			}
		}
	}
}

func TestMistakes(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--nope"}, "unknown flag: --nope (see ikeward --help)"},
		{[]string{"-x"}, "unknown shorthand flag: 'x' in -x (see ikeward --help)"},
		{[]string{"nope"}, `unknown command "nope" for "ikeward" (see ikeward --help)`},
		{[]string{"completion"}, `unknown command "completion" for "ikeward" (see ikeward --help)`},
		{[]string{"completion", "bash"},
			`unknown command "completion" for "ikeward" (see ikeward --help)`},
		{[]string{"install"},
			"install needs --p12 FILE and --sswan FILE (see ikeward install --help)"},
		{[]string{"install", "--p12", "x.p12"},
			"install needs --p12 FILE and --sswan FILE (see ikeward install --help)"},
		{[]string{"install", "--p12"}, "flag needs an argument: --p12 (see ikeward install --help)"},
		{[]string{"install", "--profile", "x.zip"},
			"unknown flag: --profile (see ikeward install --help)"},
		{[]string{"install", "--p12", "x.p12", "--sswan", "x.sswan", "y"},
			`unknown command "y" for "ikeward install" (see ikeward install --help)`},
		{[]string{"install", "--nope"}, "unknown flag: --nope (see ikeward install --help)"},
		{[]string{"uninstall", "--p12", "x.p12"},
			"unknown flag: --p12 (see ikeward uninstall --help)"},
		{[]string{"help", "nope"}, `unknown help topic "nope" (see ikeward help --help)`},
		{[]string{"help", "install", "y"},
			`unknown help topic "install y" (see ikeward help --help)`},
		{[]string{"uninstall", "y"},
			`unknown command "y" for "ikeward uninstall" (see ikeward uninstall --help)`},
		{[]string{"status", "y"},
			`unknown command "y" for "ikeward status" (see ikeward status --help)`},
		{[]string{"status", "--dry-run"},
			"unknown flag: --dry-run (see ikeward status --help)"},
		{[]string{"watchdog", "now"},
			`unknown command "now" for "ikeward watchdog" (see ikeward watchdog --help)`},
		{[]string{"watchdog", "--dry-run"},
			"unknown flag: --dry-run (see ikeward watchdog --help)"},
		{[]string{"updown", "up-client"},
			`unknown command "up-client" for "ikeward updown" (see ikeward updown --help)`},
		{[]string{"updown", "--dry-run"},
			"unknown flag: --dry-run (see ikeward updown --help)"},
		{[]string{"routing", "now"},
			`unknown command "now" for "ikeward routing" (see ikeward routing --help)`},
		{[]string{"routing", "--remove"}, "unknown flag: --remove (see ikeward routing --help)"},
	} {
		var out bytes.Buffer
		err := run(test.args, system{stdout: &out})
		if err == nil {
			t.Errorf("%q succeeded, printing %q", test.args, out.String())
			continue
		}
		if got := err.Error(); got != test.want {
			t.Errorf("%q: %q, want %q", test.args, got, test.want)
		}
		if got := exitStatus(err); got != 2 {
			t.Errorf("%q: exit status %d, want 2", test.args, got)
		}
		if out.Len() > 0 {
			t.Errorf("%q printed %q", test.args, out.String())
		}
	}
}

// Each error has its exit status, and main prints each but a changedError, which is no failure.
func TestExitStatus(t *testing.T) {
	for _, test := range []struct {
		err     error
		status  int
		message string
	}{
		{errors.New("failed"), 1, "ikeward: failed\n"},
		{usageError{errors.New("mistaken"), commandLine(system{})}, 2,
			"ikeward: mistaken (see ikeward --help)\n"},
		{watchdogError{errors.New("cannot restart")}, 3, "ikeward: cannot restart\n"},
		{changedError{}, 4, ""},
	} {
		if got := exitStatus(test.err); got != test.status {
			t.Errorf("%v: exit status %d, want %d", test.err, got, test.status)
		}
		if got := message(test.err); got != test.message {
			t.Errorf("%v: main prints %q, want %q", test.err, got, test.message)
		}
	}
}

func TestCommandHelp(t *testing.T) {
	for _, test := range []struct {
		args []string
		long string
	}{
		{[]string{"install", "--help"}, installLong},
		{[]string{"help", "install"}, installLong},
		{[]string{"uninstall", "-h"}, uninstallLong},
		{[]string{"status", "--help"}, statusLong},
		{[]string{"help", "status"}, statusLong},
		{[]string{"watchdog", "--help"}, watchdogLong},
		{[]string{"help", "watchdog"}, watchdogLong},
		{[]string{"updown", "--help"}, updownLong},
		{[]string{"help", "updown"}, updownLong},
		{[]string{"routing", "--help"}, routingLong},
		{[]string{"help", "routing"}, routingLong},
	} {
		var out bytes.Buffer
		if err := run(test.args, system{stdout: &out}); err != nil {
			t.Fatalf("%q: %v", test.args, err)
		}
		if !strings.HasPrefix(out.String(), test.long+"\n") {
			t.Errorf("%q: the help does not start with the command's text:\n%s", test.args, out.String())
		}
	}
}
