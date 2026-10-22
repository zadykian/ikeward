package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
)

const (
	// serversRange is the Internet of the test's, which holds the VPN server's address, router.
	serversRange = "198.51.100.0/24"
	// holdsServer and tooWide are why updown and ikeward routing refuse a range.
	holdsServer = "it holds " + router + ", the VPN server's address"
	tooWide     = "wider than /8, it would take the server's own traffic"
)

// serversRangeRefused has the VPN server grant its own range too, beside its two. updown refuses
// it, and learns the others: status passes, and the IKE SA still rekeys. A line of it written
// into the record by hand takes no floor as the routing's unit runs either. As a --floor, install
// refuses it, as the client's /etc/hosts resolves the server's name.
func (s *stack) serversRangeRefused(t *testing.T) {
	s.must(t, "systemctl", "stop", install.Timer, install.Service)
	cursor := s.cursor(t)
	s.grant(t, remoteRange+", "+secondRange+", "+serversRange)
	s.said(t, cursor, "Refused "+serversRange+", which the server granted: "+holdsServer)
	s.healthy(t)
	if report, want := s.must(t, files.Binary, "status"), "; refused: "+serversRange+" ("+
		holdsServer+")\n"; !strings.Contains(report, want) {
		t.Errorf("status does not say %q:\n%s", want, report)
	}
	s.rekeys(t, "with the server's range granted")
	s.unlearned(t, serversRange)

	// By hand: the routing's unit passes over the line, and status names it.
	s.must(t, "sh", "-c", `echo "$1" >>"$2"`, "sh", serversRange, routing.Learned)
	cursor = s.cursor(t)
	s.must(t, "systemctl", "restart", routing.Unit)
	s.must(t, "journalctl", "--sync")
	journal := s.must(t, "journalctl", "--no-pager", "--output=cat", "--after-cursor="+cursor,
		"--unit="+routing.Unit)
	t.Log(journal)
	if want := "Passed over " + serversRange + " in " + routing.Learned + ": " +
		holdsServer; !strings.Contains(journal, want) {
		t.Errorf("the routing's unit did not say %q", want)
	}
	if routes := s.must(t, "ip", "route", "show", "table", "220"); strings.Contains(routes,
		serversRange) || !holdsAll(routes, floors) {
		t.Errorf("with %s in the record, table 220 routes it, or lacks a floor:\n%s",
			serversRange, routes)
	}
	if report, want := s.must(t, files.Binary, "status"), "; passed over: "+serversRange+" ("+
		holdsServer+")\n"; !strings.Contains(report, want) {
		t.Errorf("status does not say %q:\n%s", want, report)
	}
	s.must(t, "sed", "-i", `\|^`+strings.ReplaceAll(serversRange, ".", `\.`)+`$|d`,
		routing.Learned)

	_, err := s.run(t, password+"\n", files.Binary, "install", "--p12", clientP12, "--sswan",
		clientSSwan, "--dns", resolver, "--domain", domain, "--floor", serversRange, "--dry-run")
	if want := "--floor " + serversRange + ": " + holdsServer; err == nil ||
		!strings.Contains(err.Error(), "exit status 2") || !strings.Contains(err.Error(), want) {
		t.Errorf("install takes --floor %s, or fails otherwise than with %q: %v", serversRange,
			want, err)
	}
	s.must(t, "systemctl", "start", install.Timer)
}

// everyAddress has the VPN server grant every address, as one that does not narrow the client's
// 0.0.0.0/0 would. updown refuses it: no route, floor or record. ownWay checks that the server's
// own traffic keeps its way, and the remote network's ranges stay unreachable, by their floors. The
// server then grants its two ranges again.
func (s *stack) everyAddress(t *testing.T) {
	s.must(t, "systemctl", "stop", install.Timer, install.Service)
	cursor := s.cursor(t)
	s.grant(t, "0.0.0.0/0")
	s.said(t, cursor, "Refused 0.0.0.0/0, which the server granted: "+tooWide)
	if policies := s.must(t, "ip", "xfrm", "policy", "list"); !strings.Contains(policies,
		"dst 0.0.0.0/0") {
		t.Errorf("no IPsec policy takes every address:\n%s", policies)
	}
	s.unlearned(t, "0.0.0.0/0")
	if routes := s.must(t, "ip", "route", "show", "table", "220"); strings.Contains(routes,
		" dev "+files.Link) || strings.Contains(routes, "default") {
		t.Errorf("with every address granted, table 220 routes through %s:\n%s", files.Link,
			routes)
	}
	s.ownWay(t)
	report, err := s.run(t, "", files.Binary, "status")
	t.Log(report)
	if want := "FAIL  route     the CHILD SA names no IPv4 range of the server's that ikeward " +
		"routes: 0.0.0.0/0 (" + tooWide + ")"; err == nil || !strings.Contains(report, want) {
		t.Errorf("with every address granted, status passes, or does not say %q: %v", want, err)
	}

	s.grant(t, remoteRange+", "+secondRange)
	s.healthy(t)
	s.must(t, "systemctl", "start", install.Timer)
}

// unlearned fails t where table 220 routes dst or makes it unreachable, or where routing.Learned
// lists it, or lacks the VPN server's address, or a floor is missing.
func (s *stack) unlearned(t *testing.T, dst string) {
	t.Helper()
	routes := s.must(t, "ip", "route", "show", "table", "220")
	t.Log(routes)
	if strings.Contains(routes, dst+" ") || !holdsAll(routes, floors) {
		t.Errorf("table 220 routes %s, makes it unreachable, or lacks a floor:\n%s", dst, routes)
	}
	ranges, servers := s.learnedRanges(t)
	for _, r := range ranges {
		if r == dst {
			t.Errorf("%s lists %s", routing.Learned, dst)
		}
	}
	if len(servers) != 1 || servers[0] != router {
		t.Errorf("%s lists the servers %q, not %s", routing.Learned, servers, router)
	}
}

// said waits until charon's lines since cursor hold updown's line want. charon may log it after
// swanctl, which connected the client, has returned.
func (s *stack) said(t *testing.T, cursor, want string) {
	t.Helper()
	eventually(t, 10*time.Second, func() error {
		if journal := s.charon(t, cursor); !strings.Contains(journal, want) {
			return fmt.Errorf("updown did not say %q:\n%s", want, journal)
		}
		return nil
	})
}

// rekeys checks that the IKE SA still rekeys, its packets leaving by the uplink; when says when.
func (s *stack) rekeys(t *testing.T, when string) {
	t.Helper()
	ike, _ := s.sas(t)
	s.must(t, "swanctl", "--rekey", "--ike", files.Connection)
	eventually(t, 30*time.Second, func() error {
		if next, _ := s.sas(t); next == ike {
			return fmt.Errorf("%s, the IKE SA %s did not rekey", when, ike)
		}
		return nil
	})
	next, _ := s.sas(t)
	t.Logf("%s, the IKE SA %s rekeyed to %s", when, ike, next)
}

// ownWay checks, with every address granted, that the IKE SA still rekeys. With a default route
// through router, the client's own traffic keeps that route, and the LAN host stays in reach. The
// remote host is unreachable, by its floor. Its names still go out ikeward0, as resolved sends
// them, and through the tunnel, as the SA's policy takes them.
func (s *stack) ownWay(t *testing.T) {
	t.Helper()
	const when = "with every address granted"
	s.rekeys(t, when)
	s.must(t, "ip", "route", "replace", "default", "via", router)
	defer s.must(t, "ip", "route", "delete", "default", "via", router)
	// 198.19.0.1, in the range for benchmarks, is no network's of the test's. Docker names the
	// uplink eth0 or eth1.
	for host, want := range map[string]string{"198.19.0.1": "via " + router + " dev eth",
		router: "dev eth"} {
		got := s.must(t, "ip", "route", "get", host)
		t.Logf("%s, ip route get %s gives %q", when, host, got)
		if !strings.HasPrefix(got, host+" "+want) || strings.Contains(got, files.Link) {
			t.Errorf("%s, ip route get %s gives %q, not %q", when, host, got, want)
		}
	}
	if _, err := s.run(t, "", "busybox", "ping", "-c", "2", "-W", "2", router); err != nil {
		t.Errorf("%s, the client's ping of %s failed: %v", when, router, err)
	}
	if r := s.reach(t, "", lanHost); !r.pinged || r.peer != onLAN {
		t.Errorf("%s, ping of the LAN host passed: %v, and TCP reached it from %q, not from %s: "+
			"%s", when, r.pinged, r.peer, onLAN, r.err)
	}
	if r := s.reach(t, "", resolver); r.pinged || !strings.Contains(r.err, unreachable) {
		t.Errorf("%s, ping of the remote host passed: %v, or TCP failed otherwise than %q: %s",
			when, r.pinged, unreachable, r.err)
	}
	query := fmt.Sprintf("query[A] %s from %s", remoteName, s.address(t))
	before := strings.Count(s.queries(t), query)
	out, err := s.run(t, "", "resolvectl", "query", "--cache=no", "--legend=no", remoteName)
	t.Log(out)
	if err != nil || !strings.Contains(out, remoteAddress) || strings.Count(s.queries(t),
		query) <= before {
		t.Errorf("%s, resolvectl query %s gives %q, %v, or the remote resolver logged no %q",
			when, remoteName, out, err, query)
	}
}
