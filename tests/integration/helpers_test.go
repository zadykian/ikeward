package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

const (
	// clientProfile is where the client has the profile, which compose.yaml mounts there.
	clientProfile = "/root/client.zip"
	// password is the .p12's, which install reads from standard input, as a script that installs
	// ikeward gives it.
	password = "integration"
)

// resolver and domain are the remote network's resolver, the far host, and its domain, which
// install's flags give. They give two floors too. given is a private range of the test's own, and
// aroundRemote holds the remote network, whose route must beat that wider floor.
const (
	resolver, domain = "192.0.2.10", "example.test"
	given            = "10.88.0.0/16"
	aroundRemote     = "192.0.2.0/23"
)

// installWith runs ikeward install as install does, with the remote network's values, and args too.
func (s *stack) installWith(t *testing.T, args ...string) {
	t.Helper()
	out, err := s.run(t, password+"\n", append([]string{files.Binary, "install", "--profile",
		clientProfile, "--dns", resolver, "--domain", domain, "--floor", given, "--floor",
		aroundRemote}, args...)...)
	t.Log(out)
	if err != nil {
		t.Fatal(err)
	}
}

// The ranges the VPN server grants: the remote network, where the far host is the resolver, and a
// second range, in 198.18.0.0/15, a range for benchmarks. The far host holds secondHost there.
const (
	remoteRange, secondRange = "192.0.2.0/24", "198.18.0.0/24"
	secondHost               = "198.18.0.10"
)

// through are updown's routes in table 220 to the ranges the server grants, as ip route shows them.
var through = []string{remoteRange + " dev " + files.Link + " ",
	secondRange + " dev " + files.Link + " "}

// floors are the floors in table 220, as ip route shows them: given's, those of the ranges the
// server grants, and aroundRemote's.
var floors = []string{floorOf(given), floorOf(remoteRange), floorOf(secondRange),
	floorOf(aroundRemote)}

// floorOf is the floor of dst, as ip route shows it.
func floorOf(dst string) string {
	return fmt.Sprintf("unreachable %s metric %d", dst, routing.FloorMetric)
}

// holdsAll is whether s holds each of parts.
func holdsAll(s string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

// routed waits until table 220 routes the ranges the server grants through ikeward0, as updown sets
// them once the tunnel is up.
func (s *stack) routed(t *testing.T) {
	t.Helper()
	eventually(t, 30*time.Second, func() error {
		out := s.must(t, "ip", "route", "show", "table", "220")
		if !holdsAll(out, through) {
			return fmt.Errorf("table 220 does not route %s and %s through %s:\n%s", remoteRange,
				secondRange, files.Link, out)
		}
		return nil
	})
}

// unrouted fails t where table 220 still routes anything through ikeward0, or lacks a floor; when
// says when.
func (s *stack) unrouted(t *testing.T, when string) {
	t.Helper()
	out := s.must(t, "ip", "route", "show", "table", "220")
	if strings.Contains(out, " dev "+files.Link+" ") || !holdsAll(out, floors) {
		t.Errorf("table 220 still routes through %s %s, or lacks a floor:\n%s", files.Link, when,
			out)
	}
}

// healthy waits until every check of ikeward status passes.
func (s *stack) healthy(t *testing.T) {
	t.Helper()
	eventually(t, time.Minute, func() error {
		_, err := s.run(t, "", files.Binary, "status")
		return err
	})
}

// show is unit's property, as systemctl shows it.
func (s *stack) show(t *testing.T, unit, property string) string {
	t.Helper()
	return strings.TrimSpace(s.must(t, "systemctl", "show", "--property="+property, "--value",
		unit))
}

// cursor is the journal's cursor, after its last entry.
func (s *stack) cursor(t *testing.T) string {
	t.Helper()
	s.must(t, "journalctl", "--sync")
	out := s.must(t, "journalctl", "--quiet", "--lines=1", "--show-cursor", "--output=cat")
	_, cursor, ok := strings.Cut(out, "-- cursor: ")
	if !ok {
		t.Fatalf("journalctl shows no cursor: %q", out)
	}
	return strings.TrimSpace(cursor)
}

// stopped checks that the connection's lines in the journal since cursor hold updown's lines on
// removing the routes as strongSwan stops, and no failure of updown's. charon logs those lines as
// it exits, and journald may not tell its unit then, so stopped reads the lines charon names
// IKE_SA_NAME itself.
func (s *stack) stopped(t *testing.T, cursor string) {
	t.Helper()
	journal := s.charon(t, cursor)
	for _, dst := range []string{remoteRange, secondRange} {
		want := "Removed the route to " + dst + " through " + files.Link + " from table 220, " +
			"as strongSwan stops"
		if !strings.Contains(journal, want) {
			t.Errorf("as strongSwan stopped, updown did not say %q:\n%s", want, journal)
		}
	}
	if strings.Contains(journal, "cannot") {
		t.Errorf("updown failed as strongSwan stopped:\n%s", journal)
	}
}

// charon is the journal's lines that charon names the connection in, those of updown among them,
// after cursor, or since boot where cursor is empty.
func (s *stack) charon(t *testing.T, cursor string) string {
	t.Helper()
	s.must(t, "journalctl", "--sync")
	args := []string{"journalctl", "--no-pager", "--output=cat", "--boot"}
	if cursor != "" {
		args = append(args, "--after-cursor="+cursor)
	}
	return s.must(t, append(args, "IKE_SA_NAME="+files.Connection)...)
}

// address is the address the server assigned the client, which strongSwan puts on ikeward0.
func (s *stack) address(t *testing.T) string {
	t.Helper()
	out := s.must(t, "ip", "-4", "-json", "address", "show", "dev", files.Link)
	var links []struct {
		Addresses []struct {
			Local string `json:"local"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal([]byte(out), &links); err != nil {
		t.Fatalf("ip address show: %v: %s", err, out)
	}
	if len(links) != 1 || len(links[0].Addresses) == 0 {
		t.Fatalf("%s has no IPv4 address: %s", files.Link, out)
	}
	return links[0].Addresses[0].Local
}
