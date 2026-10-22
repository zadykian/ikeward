// Package integration checks ikeward end to end, in Docker Compose. compose.yaml has a strongSwan
// server, a host on the remote network behind it, and a client running systemd and Docker.
// ikeward install runs on the client, with a profile from a CA made for the test.
//
// TestIntegration runs only where IKEWARD_INTEGRATION is set, as make integration sets it. It needs
// Docker with Compose, which runs the client privileged, and the kernel's modules for the tunnel,
// which no container can load.
package integration

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
)

const (
	// remoteName is a name under the remote domain, which the far host's DNS server answers with
	// remoteAddress.
	remoteName, remoteAddress = "host.example.test", "192.0.2.20"
	// otherName is no name under it: resolved must not send it to the remote resolver.
	otherName = "example.org"
)

// TestIntegration installs ikeward on a fresh client, and checks the tunnel, the ranges learned,
// the resolver and status's probe. The names follow as strongSwan stops and the link's own address
// goes. NAT, a forwarded network, the watchdog, the routes' end, grants refused, the LAN and the
// floors come next, then a reboot and uninstall. Each step starts where the last left the client.
func TestIntegration(t *testing.T) {
	if os.Getenv("IKEWARD_INTEGRATION") == "" {
		t.Skip("runs in Docker Compose, with a privileged container: make integration")
	}
	s := up(t)
	// && runs no step after one that failed.
	_ = t.Run("install", s.install) &&
		t.Run("tunnel", s.tunnel) &&
		t.Run("learned ranges", s.learned) &&
		t.Run("resolver", s.resolver) &&
		t.Run("resolver bound to ikeward0", s.resolverBound) &&
		t.Run("names with strongSwan stopped", s.namesStopped) &&
		t.Run("own address gone", s.ownAddressGone) &&
		t.Run("NAT", s.nat) &&
		t.Run("watchdog", s.watchdog) &&
		t.Run("NAT after the restart", s.nat) &&
		t.Run("forwarded network", s.forwarded) &&
		t.Run("NAT without the rule", s.withoutRule) &&
		t.Run("watchdog makes the routing", s.watchdogRouting) &&
		t.Run("watchdog fails", s.watchdogFails) &&
		t.Run("strongSwan stops", s.strongSwanStops) &&
		t.Run("tunnel down and up", s.tunnelDown) &&
		t.Run("the server's range refused", s.serversRangeRefused) &&
		t.Run("every address refused", s.everyAddress) &&
		t.Run("own networks first", s.ownFirst) &&
		t.Run("floor with strongSwan stopped", s.floorDown) &&
		t.Run("reboot", s.reboot) &&
		t.Run("uninstall", s.uninstall)
}

// install runs ikeward install with the profile, and the .p12's password on standard input, on a
// fresh client. Its strongSwan runs as the package ships it, with charon's rule for table 220. That
// rule must go, with another of that table the step adds. networkd must make files.Link, the
// tunnel's XFRM interface, with files.InterfaceID.
func (s *stack) install(t *testing.T) {
	eventually(t, 30*time.Second, func() error {
		if rules := s.must(t, "ip", "rule", "show", "table", "220"); !strings.HasPrefix(rules,
			"220:") {
			return fmt.Errorf("no rule of strongSwan's looks up table 220:\n%s", rules)
		}
		return nil
	})
	// A charon that stops cleanly removes its own rule, as install restarts strongSwan. So a rule
	// charon doesn't own, with no suppress_prefixlength, checks routing.Foreign against a kernel.
	s.must(t, "ip", "rule", "add", "pref", "221", "lookup", "220")
	s.installWith(t)
	if rules := s.must(t, "ip", "rule", "show", "table", "220"); !strings.Contains(rules,
		"32001:") || strings.Contains(rules, "220:") || strings.Contains(rules, "221:") {
		t.Errorf("the rules that look up table 220 are not ikeward's alone:\n%s", rules)
	}
	want := fmt.Sprintf("xfrm if_id %#x ", files.InterfaceID)
	if link := s.must(t, "ip", "-details", "link", "show", files.Link); !strings.Contains(link,
		want) {
		t.Errorf("%s is not an XFRM interface with if_id %#x:\n%s", files.Link, files.InterfaceID,
			link)
	}
}

// tunnel waits until every check of ikeward status passes, then checks that install enabled and
// started the routing's unit, strongSwan and the watchdog's timer. strongSwan must run without its
// resolve plugin, which ikeward's drop-in turns off. Its policies must take files.InterfaceID.
func (s *stack) tunnel(t *testing.T) {
	var status string
	eventually(t, time.Minute, func() (err error) {
		status, err = s.run(t, "", files.Binary, "status")
		return err
	})
	t.Log(status)
	// Given several units, is-enabled and is-active pass where any one does.
	for _, unit := range []string{routing.Unit, install.StrongSwan, install.Timer} {
		s.must(t, "systemctl", "is-enabled", "--quiet", unit)
		s.must(t, "systemctl", "is-active", "--quiet", unit)
	}

	stats := s.must(t, "swanctl", "--stats")
	_, plugins, _ := strings.Cut(stats, "loaded plugins:")
	plugins, _, _ = strings.Cut(plugins, "\n")
	loaded := strings.Fields(plugins)
	if !slices.Contains(loaded, "vici") {
		t.Fatalf("strongSwan did not load its vici plugin:%s", plugins)
	}
	if slices.Contains(loaded, "resolve") {
		t.Errorf("strongSwan loaded its resolve plugin, which zz-ikeward.conf turns off:%s", plugins)
	}
	policies := s.must(t, "ip", "xfrm", "policy", "list")
	if want := fmt.Sprintf("if_id %#x", files.InterfaceID); !strings.Contains(policies, want) {
		t.Errorf("no IPsec policy has %s:\n%s", want, policies)
	}
}

// resolver checks that resolved sends a lookup of a remote name to the remote resolver,
// which answers it through the tunnel from the address the server assigned. resolved must send no
// other name there.
func (s *stack) resolver(t *testing.T) {
	out := s.must(t, "resolvectl", "query", "--legend=no", remoteName)
	t.Log(out)
	if !strings.Contains(out, remoteAddress) {
		t.Errorf("resolvectl query %s gives %q, not %s", remoteName, out, remoteAddress)
	}
	if out, err := s.run(t, "", "resolvectl", "query", "--legend=no", otherName); err == nil {
		t.Errorf("resolved found %s, where no server should answer it: %s", otherName, out)
	}
	queries := s.queries(t)
	want := fmt.Sprintf("query[A] %s from %s", remoteName, s.address(t))
	if !strings.Contains(queries, want) {
		t.Errorf("the remote resolver did not log %q:\n%s", want, queries)
	}
	t.Logf("the remote resolver logged %q", want)
	if strings.Contains(queries, otherName) {
		t.Errorf("resolved sent %s to the remote resolver:\n%s", otherName, queries)
	}
}

// offTunnel is the far host's second address, where its DNS server answers too. The VPN server
// routes it, as the uplink's router, but grants none of it: the client reaches it in the clear.
const offTunnel = "198.18.1.53"

// resolverBound has status ask offTunnel, which a lookup by a default route through router
// reaches. status's lookup leaves by ikeward0 alone, where no SA takes it: its resolver check
// fails, and the far host logs no query of it. The watchdog's timer stops meanwhile, as a run of
// the watchdog could restart strongSwan, or ask the far host too.
func (s *stack) resolverBound(t *testing.T) {
	s.must(t, "systemctl", "stop", install.Timer, install.Service)
	defer s.must(t, "systemctl", "start", install.Timer)
	s.must(t, "ip", "route", "replace", "default", "via", router)
	defer s.must(t, "ip", "route", "delete", "default", "via", router)
	if out := s.must(t, "busybox", "nslookup", "-type=a", domain, offTunnel); !strings.Contains(
		out, resolver) {
		t.Fatalf("a lookup of %s through %s in the clear gives %q, not %s", domain, offTunnel,
			out, resolver)
	}
	asked := "query[A] " + domain + " from "
	before := strings.Count(s.queries(t), asked)
	settings := s.must(t, "cat", files.SettingsFile)
	s.must(t, "sed", "-i", "s/^dns .*/dns "+offTunnel+"/", files.SettingsFile)
	report, err := s.run(t, "", files.Binary, "status")
	s.must(t, "sh", "-c", `printf %s "$1" >"$2"`, "sh", settings, files.SettingsFile)
	t.Log(report)
	line := regexp.MustCompile(`(?m)^FAIL +resolver +` + regexp.QuoteMeta(offTunnel) +
		` gives no address .*$`).FindString(report)
	if err == nil || line == "" || strings.Contains(line, "through the tunnel") {
		t.Errorf("status's resolver check passes, or got an answer, with %s: %v", offTunnel, err)
	}
	if after := strings.Count(s.queries(t), asked); after != before {
		t.Errorf("the far host logged %d queries for %s from the client during status, not 0",
			after-before, domain)
	}
	s.healthy(t)
}

// uninstall runs ikeward uninstall with the tunnel up, and ikeward's routing in place. It checks
// that uninstall leaves no NAT table, none of ikeward's rules, no route in table 220, and no
// files.Link, and that strongSwan is stopped and disabled. systemd must no longer have the
// watchdog's units or the routing's. The files install wrote must be gone, with both records.
func (s *stack) uninstall(t *testing.T) {
	var paths []string
	for line := range strings.Lines(s.must(t, "cat", install.Record)) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			paths = append(paths, line)
		}
	}
	if len(paths) == 0 {
		t.Fatalf("%s lists no file", install.Record)
	}
	// The watchdog records its restarts, which uninstall removes too.
	s.must(t, "sh", "-c", `date -u +%Y-%m-%dT%H:%M:%SZ >"$1"`, "sh", install.LastRestart)
	paths = append(paths, install.Record, install.LastRestart, routing.Learned)

	t.Log(s.must(t, files.Binary, "uninstall"))
	if out, err := s.run(t, "", "nft", "list", "table", "ip", routing.NATTable); err == nil {
		t.Errorf("table ip %s is still there after uninstall:\n%s", routing.NATTable, out)
	}
	if rules := s.must(t, "ip", "rule"); strings.Contains(rules, "32000:") ||
		strings.Contains(rules, "lookup 220") {
		t.Errorf("ikeward's rules are still there after uninstall:\n%s", rules)
	}
	if routes := s.must(t, "ip", "route", "show", "table", "220"); routes != "" {
		t.Errorf("table 220 still routes after uninstall:\n%s", routes)
	}
	if out, err := s.run(t, "", "ip", "link", "show", files.Link); err == nil {
		t.Errorf("%s is still there after uninstall:\n%s", files.Link, out)
	}
	if got := s.show(t, install.StrongSwan, "ActiveState"); got != "inactive" {
		t.Errorf("strongSwan is %s after uninstall, not inactive", got)
	}
	if got := s.show(t, install.StrongSwan, "UnitFileState"); got != "disabled" {
		t.Errorf("strongSwan is %s after uninstall, not disabled", got)
	}
	for _, unit := range []string{install.Timer, install.Service, routing.Unit} {
		if got := s.show(t, unit, "LoadState"); got != "not-found" {
			t.Errorf("systemd has %s after uninstall, %s", unit, got)
		}
	}
	left := s.must(t, append([]string{"sh", "-c", `for p; do [ ! -e "$p" ] || echo "$p"; done`,
		"sh"}, paths...)...)
	if left != "" {
		t.Errorf("uninstall left:\n%s", left)
	}
}
