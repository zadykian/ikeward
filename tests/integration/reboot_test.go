package integration

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
)

// reboot restarts the client, as the server would reboot, and checks the order of its boot. A
// drop-in of strongSwan's has ip and nft list the routing into witness as strongSwan starts. The
// tunnel must come up again.
func (s *stack) reboot(t *testing.T) {
	const witness = "/run/routing-as-strongswan-starts"
	dropIn := "/etc/systemd/system/" + install.StrongSwan + ".d/zz-test-routing.conf"
	s.must(t, "sh", "-c", `mkdir -p "${1%/*}" && rm -f "$2" && printf '%s\n' '[Service]' `+
		`"ExecStartPre=-/bin/sh -c '{ ip rule; ip route show table 220; `+
		`nft list table ip $3; } >$2 2>&1'" >"$1"`, "sh", dropIn, witness, routing.NATTable)
	ctx, cancel := context.WithTimeout(s.ctx, commandTimeout)
	defer cancel()
	if _, err := s.compose(ctx, "", "restart", "--timeout", "60", "client"); err != nil {
		t.Fatal(err)
	}
	// It fails while the client restarts, and where a unit failed as it booted.
	eventually(t, 2*time.Minute, func() error {
		_, err := s.run(t, "", "systemctl", "is-system-running", "--wait")
		return err
	})
	s.bootOrder(t, witness)
	s.must(t, "rm", "-f", dropIn, witness)
	s.must(t, "systemctl", "daemon-reload")
	s.healthy(t)
}

// bootOrder checks that the routing's unit made the rules, the floors and the NAT table before
// networkd and strongSwan started, with a floor for each learned range. systemd must order them so,
// with no ordering cycle, and its timestamps of this boot must show it. As strongSwan started,
// witness got what ip and nft listed.
func (s *stack) bootOrder(t *testing.T, witness string) {
	t.Helper()
	for unit, after := range map[string]string{install.StrongSwan: routing.Unit,
		"network-pre.target": routing.Unit, "systemd-networkd.service": "network-pre.target",
		routing.Unit: "nftables.service"} {
		if got := strings.Fields(s.show(t, unit, "After")); !slices.Contains(got, after) {
			t.Errorf("systemd does not order %s after %s: %q", unit, after, got)
		}
	}
	if got := s.show(t, routing.Unit, "Result"); got != "success" {
		t.Errorf("the routing's unit's result is %s, not success", got)
	}
	made := s.monotonic(t, routing.Unit, "ActiveEnterTimestampMonotonic")
	for _, unit := range []string{"systemd-networkd.service", install.StrongSwan} {
		started := s.monotonic(t, unit, "InactiveExitTimestampMonotonic")
		t.Logf("the routing was made at %d µs, and %s started at %d µs", made, unit, started)
		if made == 0 || started < made {
			t.Errorf("%s started before the routing was made", unit)
		}
	}
	// ip rule puts a tab after a rule's priority.
	listed := strings.ReplaceAll(s.must(t, "cat", witness), ":\t", ": ")
	t.Logf("as strongSwan started, ip and nft listed:\n%s", listed)
	for _, want := range append([]string{routing.Rules[0].String(), routing.Rules[1].String(),
		strings.TrimSuffix(routing.NATRule, " masquerade"), "masquerade"}, floors...) {
		if !strings.Contains(listed, want) {
			t.Errorf("as strongSwan started, the routing lacked %q", want)
		}
	}
	journal := s.must(t, "journalctl", "--boot", "--no-pager", "--output=cat",
		"--priority=warning")
	if strings.Contains(journal, "ordering cycle") {
		t.Errorf("systemd found an ordering cycle:\n%s", journal)
	}
}

// monotonic is unit's property, a time in microseconds since boot, as systemctl shows it.
func (s *stack) monotonic(t *testing.T, unit, property string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s.show(t, unit, property), 10, 64)
	if err != nil {
		t.Fatal(fmt.Errorf("%s's %s: %w", unit, property, err))
	}
	return n
}
