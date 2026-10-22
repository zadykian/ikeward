package integration

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/status"
)

const (
	// outageName is a remote name that the test looks up first with strongSwan stopped.
	outageName = "outage.example.test"
	// foundWithin bounds how long resolved takes to find outageName once the tunnel is back. It
	// tries about every 10 seconds, and gives up on a lookup after 2 minutes.
	foundWithin = 30 * time.Second
)

// namesStopped stops strongSwan, which takes its address off ikeward0, and the watchdog's timer,
// whose run could start it again. ikeward0 keeps its DNS scope by its own address. So a lookup of a
// remote name goes out ikeward0 alone, which drops it, as no SA takes it. None reaches the
// remote resolver, and resolved finds that name once strongSwan is back, as docs/status.md says.
func (s *stack) namesStopped(t *testing.T) {
	s.must(t, "systemctl", "stop", install.Timer, install.Service, install.StrongSwan)
	eventually(t, 10*time.Second, func() error {
		out := s.must(t, "ip", "-4", "-oneline", "address", "show", "dev", files.Link)
		if out != "" {
			return fmt.Errorf("%s keeps an IPv4 address with strongSwan stopped:\n%s", files.Link,
				out)
		}
		return nil
	})
	if out, ok := s.dnsScope(t); !ok {
		t.Errorf("resolved gives %s no DNS scope with strongSwan stopped:\n%s", files.Link, out)
	}
	// status fails, the tunnel being down, but its DNS check passes.
	report, err := s.run(t, "", files.Binary, "status")
	t.Log(report)
	if err == nil || !strings.HasPrefix(dnsLine(report), "ok ") {
		t.Errorf("with strongSwan stopped, status passes, or its %s check fails: %v",
			status.DNSCheck, err)
	}

	// A default route through router, as on a server, would take resolved's retries over TCP,
	// which ignore ikeward0, but the floor makes the remote network unreachable.
	s.must(t, "ip", "route", "replace", "default", "via", router)
	before := s.linkDrops(t)
	onLink, elsewhere := s.lookupCounts(t, outageName)
	if onLink == 0 || elsewhere != 0 {
		t.Errorf("with strongSwan stopped, %d queries for %s were routed out %s, and %d out other "+
			"links; want some out %s alone", onLink, outageName, files.Link, elsewhere, files.Link)
	}
	dropped := s.linkDrops(t) - before
	if dropped < onLink {
		t.Errorf("with strongSwan stopped, %s dropped %d packets, fewer than the %d queries for %s",
			files.Link, dropped, onLink, outageName)
	}
	if queries := s.queries(t); strings.Contains(queries, outageName) {
		t.Errorf("with strongSwan stopped, the remote resolver logged %s:\n%s", outageName,
			queries)
	}
	t.Logf("with strongSwan stopped, %d queries for %s were routed out %s, and none out other "+
		"links; %s dropped %d packets", onLink, outageName, files.Link, files.Link, dropped)

	s.must(t, "systemctl", "start", install.StrongSwan)
	s.healthy(t)
	s.foundAgain(t)
	s.must(t, "ip", "route", "delete", "default", "via", router)
	s.must(t, "systemctl", "start", install.Timer)
}

// ownAddressGone deletes ikeward0's own address with the tunnel up. resolved keeps its DNS scope by
// strongSwan's address, but the DNS check fails, alone, and names the address. networkctl
// reconfigure puts it back, as docs/status.md says, and leaves strongSwan's address in place.
func (s *stack) ownAddressGone(t *testing.T) {
	vip := s.address(t)
	s.must(t, "ip", "address", "delete", files.LinkULA, "dev", files.Link)
	report, err := s.run(t, "", files.Binary, "status")
	t.Log(report)
	if line := dnsLine(report); err == nil || strings.Count(report, "FAIL") != 1 ||
		!strings.HasPrefix(line, "FAIL ") || !strings.Contains(line, files.LinkULA) {
		t.Errorf("without %s on %s, status passes, or fails other checks than %s, or that one "+
			"does not name the address: %v", files.LinkULA, files.Link, status.DNSCheck, err)
	}
	if out, ok := s.dnsScope(t); !ok {
		t.Errorf("resolved gives %s no DNS scope with strongSwan's address on it:\n%s", files.Link,
			out)
	}

	s.must(t, "networkctl", "reconfigure", files.Link)
	s.healthy(t)
	if got := s.address(t); got != vip {
		t.Errorf("after networkctl reconfigure, %s has %s, not %s", files.Link, got, vip)
	}
}

// linkDrops is how many packets files.Link has dropped, as no SA took them, since networkd made
// it.
func (s *stack) linkDrops(t *testing.T) int {
	t.Helper()
	var links []struct {
		Stats struct {
			TX struct {
				Dropped int `json:"dropped"`
			} `json:"tx"`
		} `json:"stats64"`
	}
	out := s.must(t, "ip", "-json", "-statistics", "link", "show", "dev", files.Link)
	if err := json.Unmarshal([]byte(out), &links); err != nil || len(links) != 1 {
		t.Fatalf("ip link show %s: %v: %s", files.Link, err, out)
	}
	return links[0].Stats.TX.Dropped
}

// dnsScope is whether resolved gives files.Link a DNS scope, and what resolvectl said of the link.
func (s *stack) dnsScope(t *testing.T) (string, bool) {
	t.Helper()
	type scope struct {
		Protocol string `json:"protocol"`
	}
	var links []struct {
		Scopes []scope `json:"scopes"`
	}
	out := s.must(t, "resolvectl", "--json=short", "status", files.Link)
	err := json.Unmarshal([]byte(out), &links)
	return out, err == nil && len(links) == 1 &&
		slices.Contains(links[0].Scopes, scope{Protocol: "dns"})
}

// dnsLine is the line of report, what ikeward status printed, of its DNS check.
func dnsLine(report string) string {
	return regexp.MustCompile(`(?m)^\S+ +` + status.DNSCheck + ` .*$`).FindString(report)
}

// foundAgain checks that resolved finds outageName through the tunnel within foundWithin, with
// the link's scope and the server's state it kept through the outage. The lookup that
// lookupCounts left pending goes through at resolved's next try, and this one with it.
func (s *stack) foundAgain(t *testing.T) {
	t.Helper()
	start := time.Now()
	out := s.must(t, "resolvectl", "query", "--legend=no", outageName)
	took := time.Since(start).Round(time.Millisecond)
	if !strings.Contains(out, remoteAddress) || took > foundWithin {
		t.Errorf("with strongSwan started again, resolvectl query %s gives %q after %v", outageName,
			out, took)
	}
	want := fmt.Sprintf("query[A] %s from %s", outageName, s.address(t))
	if queries := s.queries(t); !strings.Contains(queries, want) {
		t.Errorf("the remote resolver did not log %q:\n%s", want, queries)
	}
	t.Logf("with strongSwan started again, resolved found %s after %v, and the remote "+
		"resolver logged %q", outageName, took, want)
}

// lookupCounts looks name up through resolved, which must fail, and returns how many queries for
// it were routed out files.Link, and out other links. A table of nftables counts them by the name,
// past the UDP and DNS headers' 20 bytes. Its chain comes before Docker's DNAT, which moves queries
// to its resolver, the client's global server, off port 53. resolvectl gets 5 seconds of retries.
func (s *stack) lookupCounts(t *testing.T, name string) (onLink, elsewhere int) {
	t.Helper()
	var wire []byte
	for label := range strings.SplitSeq(name, ".") {
		wire = append(append(wire, byte(len(label))), label...)
	}
	wire = append(wire, 0)
	// nftables refuses a raw match of more than 16 bytes, so the name takes one per 16 bytes.
	match := "udp dport 53"
	for i := 0; i < len(wire); i += 16 {
		part := wire[i:min(i+16, len(wire))]
		match += fmt.Sprintf(" @th,%d,%d 0x%x", 160+8*i, 8*len(part), part)
	}
	ruleset := fmt.Sprintf("table inet names {\n chain output {\n"+
		"  type filter hook output priority raw; policy accept;\n"+
		"  %[1]s oifname %[2]q counter comment \"link\"\n"+
		"  %[1]s oifname != %[2]q counter comment \"elsewhere\"\n }\n}\n", match, files.Link)
	if out, err := s.run(t, ruleset, "nft", "-f", "-"); err != nil {
		t.Fatalf("nft -f: %v\n%s", err, out)
	}
	out, err := s.run(t, "", "timeout", "5", "resolvectl", "query", "--cache=no", "--legend=no",
		name)
	if err == nil || strings.Contains(out, remoteAddress) {
		t.Errorf("resolved found %s with strongSwan stopped: %s", name, out)
	}
	chain := s.must(t, "nft", "list", "chain", "inet", "names", "output")
	s.must(t, "nft", "delete", "table", "inet", "names")
	counts := map[string]int{}
	for _, m := range regexp.MustCompile(`counter packets (\d+) bytes \d+ comment "(\w+)"`).
		FindAllStringSubmatch(chain, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		counts[m[2]] = n
	}
	if len(counts) != 2 {
		t.Fatalf("nftables shows no counters:\n%s", chain)
	}
	return counts["link"], counts["elsewhere"]
}
