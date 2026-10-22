package integration

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
)

// watchdog removes the address the server assigned from ikeward0, and runs the watchdog's service,
// as its timer does. The watchdog must restart strongSwan, which sets the address again, and log
// each line as a warning.
func (s *stack) watchdog(t *testing.T) {
	// No run of the timer's may overlap the test's. Its first, 3 minutes after the client's
	// systemd started, may have restarted strongSwan, which keeps the watchdog from another
	// restart for 10 minutes, as its record says.
	s.must(t, "systemctl", "stop", install.Timer, install.Service)
	s.must(t, "rm", "-f", install.LastRestart)
	// The restart starts with the route in place. As strongSwan stops, updown removes it without
	// asking strongSwan, which no longer answers, and sets it again as the tunnel comes up.
	s.routed(t)
	cursor := s.cursor(t)
	s.must(t, "ip", "address", "delete", s.address(t)+"/32", "dev", files.Link)
	if out, err := s.run(t, "", files.Binary, "status"); err == nil {
		t.Fatalf("ikeward status passes with no address on %s:\n%s", files.Link, out)
	}

	s.must(t, "systemctl", "start", install.Service)
	// The journal of this run alone: the timer's runs before it may have restarted strongSwan.
	journal := s.warnings(t, s.show(t, install.Service, "InvocationID"))
	if got := s.show(t, install.Service, "ExecMainStatus"); got != "0" {
		t.Errorf("the watchdog exited %s, not 0", got)
	}
	if !strings.Contains(journal, "systemctl restart --no-block "+install.StrongSwan) {
		t.Error("the watchdog did not restart strongSwan")
	}
	s.stopped(t, cursor)
	s.must(t, files.Binary, "status")
	s.must(t, "systemctl", "start", install.Timer)
}

// watchdogFails has the watchdog fail itself: with install.LastRestart a directory, it cannot
// tell when it last restarted strongSwan. It leaves strongSwan alone and exits 3, which its
// service, unlike 1, counts as a failure, and logs each line as a warning. A restart of strongSwan
// by hand then sets the address again.
func (s *stack) watchdogFails(t *testing.T) {
	s.must(t, "systemctl", "stop", install.Timer, install.Service)
	s.must(t, "rm", "-f", install.LastRestart)
	s.must(t, "mkdir", install.LastRestart)
	s.must(t, "ip", "address", "delete", s.address(t)+"/32", "dev", files.Link)

	if out, err := s.run(t, "", "systemctl", "start", install.Service); err == nil {
		t.Fatalf("the watchdog's service did not fail:\n%s", out)
	}
	journal := s.warnings(t, s.show(t, install.Service, "InvocationID"))
	if got := s.show(t, install.Service, "ActiveState"); got != "failed" {
		t.Errorf("the watchdog's service is %s, not failed", got)
	}
	if got := s.show(t, install.Service, "ExecMainStatus"); got != "3" {
		t.Errorf("the watchdog exited %s, not 3", got)
	}
	const cannot = "cannot tell when the watchdog last restarted strongSwan"
	if !strings.Contains(journal, cannot) {
		t.Errorf("the watchdog's journal lacks %q", cannot)
	}
	if strings.Contains(journal, "systemctl restart") {
		t.Error("the watchdog restarted strongSwan, though it could not read its record of the " +
			"last restart")
	}

	s.must(t, "rmdir", install.LastRestart)
	s.must(t, "systemctl", "reset-failed", install.Service)
	s.must(t, "systemctl", "restart", install.StrongSwan)
	s.healthy(t)
	s.must(t, "systemctl", "start", install.Timer)
}

// watchdogRouting runs the watchdog's service with the tunnel up. The NAT table is gone, as
// withoutRule left it, with the rule for main, given's floor and a learned range's.
// The watchdog must make each again and say so, and leave strongSwan alone: the IKE SA and the
// CHILD SA stay. A container on forwarded then reaches the remote network again.
func (s *stack) watchdogRouting(t *testing.T) {
	s.must(t, "systemctl", "stop", install.Timer, install.Service)
	s.must(t, "rm", "-f", install.LastRestart)
	ike, child := s.sas(t)
	for _, dst := range []string{given, secondRange} {
		s.must(t, "ip", "route", "delete", "unreachable", dst, "table", "220", "metric",
			strconv.Itoa(routing.FloorMetric))
	}
	s.must(t, "ip", "rule", "delete", "pref", strconv.Itoa(routing.Rules[0].Priority))
	if out, err := s.run(t, "", files.Binary, "status"); err == nil {
		t.Fatalf("ikeward status passes without the rule, the floor and the NAT table:\n%s", out)
	}

	s.must(t, "systemctl", "start", install.Service)
	journal := s.warnings(t, s.show(t, install.Service, "InvocationID"))
	if got := s.show(t, install.Service, "ExecMainStatus"); got != "0" {
		t.Errorf("the watchdog exited %s, not 0", got)
	}
	for _, want := range []string{"Made table ip " + routing.NATTable + ": " + routing.NATRule,
		"Made " + given + " unreachable in table 220: " + floors[0],
		"Made " + secondRange + " unreachable in table 220: " + floors[2],
		"Added rule " + routing.Rules[0].String(),
		"Every check passes, with what was missing made again"} {
		if !strings.Contains(journal, want) {
			t.Errorf("the watchdog's journal lacks %q", want)
		}
	}
	if strings.Contains(journal, "systemctl restart") {
		t.Error("the watchdog restarted strongSwan for the routing")
	}
	s.must(t, files.Binary, "status")
	if nextIKE, nextChild := s.sas(t); nextIKE != ike || nextChild != child {
		t.Errorf("the IKE SA went from %s to %s, and the CHILD SA from %s to %s", ike, nextIKE,
			child, nextChild)
	}
	if from, status := s.probe(t, forwarded); status != 0 || from != s.address(t) {
		t.Errorf("a container on %s exited %d, and reached the remote network from %q, not "+
			"from %s", forwarded, status, from, s.address(t))
	}
	s.must(t, "systemctl", "start", install.Timer)
}

// warnings is the journal of the watchdog's run invocation, whose lines must each be a warning,
// as SyslogLevel= has them, and of which there must be one at least.
func (s *stack) warnings(t *testing.T, invocation string) string {
	t.Helper()
	s.must(t, "journalctl", "--sync")
	out := s.must(t, "journalctl", "--no-pager", "--output=json",
		"--output-fields=PRIORITY,MESSAGE", "_SYSTEMD_INVOCATION_ID="+invocation)
	var journal strings.Builder
	for line := range strings.Lines(out) {
		var entry struct {
			Priority string `json:"PRIORITY"`
			Message  string `json:"MESSAGE"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("journalctl: %v: %s", err, line)
		}
		if entry.Priority != "4" {
			t.Errorf("the watchdog logged %q at priority %s, not 4, a warning", entry.Message,
				entry.Priority)
		}
		journal.WriteString(entry.Message + "\n")
	}
	t.Log(journal.String())
	if journal.Len() == 0 {
		t.Fatal("the watchdog's run logged nothing")
	}
	return journal.String()
}
