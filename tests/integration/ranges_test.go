package integration

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// learned checks what updown learned as the tunnel first came up. Table 220 routes both ranges the
// server grants through ikeward0, above a floor each. routing.Learned lists them, and the VPN
// server's address. The routing's unit, which install ran before strongSwan started, made only
// the floors install was given, and updown the others. status names the ranges.
func (s *stack) learned(t *testing.T) {
	routes := s.must(t, "ip", "route", "show", "table", "220")
	t.Log(routes)
	if !holdsAll(routes, append(slices.Clone(through), floors...)) {
		t.Errorf("table 220 lacks a route through %s or a floor:\n%s", files.Link, routes)
	}
	ranges, servers := s.learnedRanges(t)
	if !slices.Equal(ranges, []string{remoteRange, secondRange}) ||
		!slices.Equal(servers, []string{router}) {
		t.Errorf("%s lists %q, and the servers %q; not %s and %s, and %s", routing.Learned,
			ranges, servers, remoteRange, secondRange, router)
	}
	journal := s.must(t, "journalctl", "--boot", "--no-pager", "--output=cat", "--unit="+routing.Unit)
	if strings.Count(journal, " unreachable in table 220") != 2 || !strings.Contains(journal,
		"Made "+given+" unreachable") || !strings.Contains(journal,
		"Made "+aroundRemote+" unreachable") {
		t.Errorf("the routing's unit made other floors than those install was given:\n%s",
			journal)
	}
	charon := s.charon(t, "")
	for _, dst := range []string{remoteRange, secondRange} {
		if want := "Learned " + dst + ", which " + routing.Learned + " records"; !strings.Contains(
			charon, want) {
			t.Errorf("updown did not say %q:\n%s", want, charon)
		}
	}
	report := s.must(t, files.Binary, "status")
	t.Log(report)
	both := remoteRange + ", " + secondRange
	for _, want := range []string{"table 220 routes " + both + " through " + files.Link,
		"; learned: " + both + "\n"} {
		if !strings.Contains(report, want) {
			t.Errorf("status does not say %q", want)
		}
	}
}

// learnedRanges are the ranges routing.Learned lists, and the VPN server's addresses it lists.
func (s *stack) learnedRanges(t *testing.T) (ranges, servers []string) {
	t.Helper()
	for line := range strings.Lines(s.must(t, "cat", routing.Learned)) {
		line = strings.TrimSpace(line)
		if server, ok := strings.CutPrefix(line, "server "); ok {
			servers = append(servers, server)
		} else if line != "" && !strings.HasPrefix(line, "#") {
			ranges = append(ranges, line)
		}
	}
	return ranges, servers
}

// grant has the VPN server grant ranges, the value of its child's local_ts, and the client
// connect anew. The server takes the change for the CHILD SAs it makes from then on.
func (s *stack) grant(t *testing.T, ranges string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, commandTimeout)
	defer cancel()
	if _, err := s.compose(ctx, "", "exec", "-T", "server", "sh", "-ec",
		`sed -i "s|^\( *local_ts *= \).*|\1$1|" /etc/swanctl/conf.d/server.conf
swanctl --load-conns`, "sh", ranges); err != nil {
		t.Fatal(err)
	}
	s.must(t, "swanctl", "--terminate", "--ike", files.Connection)
	t.Log(s.must(t, "swanctl", "--initiate", "--child", files.Connection))
}
