package integration

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
)

const (
	// peerPort is the far host's port that answers a connection with the address it came from.
	peerPort = "8080"
	// laterNetwork is a Docker network made in the client once the tunnel is up.
	laterNetwork = "later"
	// forwarded is a Docker network in the client on forwardedSubnet, a private range the test
	// picks, apart from the others. It stands in for a network the server forwards, as WireGuard's.
	forwarded, forwardedSubnet = "forwarded", "10.89.0.0/24"
	// uplinkNAT is the client's NAT of forwardedSubnet to the uplink's address, out the link it
	// names, as a server's firewall has for its WireGuard network. It NATs nothing that leaves by
	// ikeward0.
	uplinkNAT = "table ip uplink {\n chain postrouting {\n" +
		"  type nat hook postrouting priority srcnat; policy accept;\n" +
		"  ip saddr " + forwardedSubnet + " oifname %q masquerade\n }\n}\n"
)

// forwardedNetwork makes forwarded, where the client lacks it, and uplinkNAT. Docker NATs none of
// its traffic, so only ikeward's rule NATs what it sends through the tunnel, as for WireGuard's.
// Docker's MASQUERADE takes any link's address, ikeward0's too, which the tunnel takes.
func (s *stack) forwardedNetwork(t *testing.T) {
	t.Helper()
	s.must(t, "sh", "-c", "docker network inspect "+forwarded+" >/dev/null 2>&1 || "+
		"docker network create --subnet "+forwardedSubnet+
		" -o com.docker.network.bridge.enable_ip_masquerade=false "+forwarded)
	if _, err := s.run(t, "", "nft", "list", "table", "ip", "uplink"); err == nil {
		return
	}
	// Docker names the uplink eth0 or eth1, whatever compose.yaml's priority says: one run named
	// it eth1.
	fields := strings.Fields(s.must(t, "ip", "-oneline", "-4", "address", "show", "to", uplink))
	if len(fields) < 2 {
		t.Fatalf("no link holds %s", uplink)
	}
	if out, err := s.run(t, fmt.Sprintf(uplinkNAT, fields[1]), "nft", "-f", "-"); err != nil {
		t.Fatalf("nft -f: %v\n%s", err, out)
	}
}

// nat checks that containers reach the remote network from the address the server assigned.
// Those on Docker's default network do, and so do those on a network made once the tunnel was up,
// whose NAT rule Docker added after ikeward's.
func (s *stack) nat(t *testing.T) {
	address := s.address(t)
	s.must(t, "sh", "-c", "docker network inspect "+laterNetwork+" >/dev/null 2>&1 || "+
		"docker network create "+laterNetwork)
	for _, network := range []string{"bridge", laterNetwork} {
		from, status := s.probe(t, network)
		if status != 0 {
			t.Fatalf("a container on Docker's network %s exited %d", network, status)
		}
		if from != address {
			t.Errorf("a container on Docker's network %s reached the remote network from %q, "+
				"not from %s", network, from, address)
		}
		t.Logf("a container on Docker's network %s reached the remote network from %s",
			network, from)
	}
	t.Log(s.must(t, "nft", "list", "table", "ip", routing.NATTable))
}

// forwarded checks that a container on forwarded, which stands in for a network the server
// forwards, reaches the remote network through ikeward's NAT rule. It must come from the
// address the server assigned.
func (s *stack) forwarded(t *testing.T) {
	address := s.address(t)
	s.forwardedNetwork(t)
	if from, status := s.probe(t, forwarded); status != 0 || from != address {
		t.Errorf("a container on Docker's network %s exited %d, and reached the remote "+
			"network from %q, not from %s", forwarded, status, from, address)
	}
}

// sas are the unique IDs of the connection's IKE SA and CHILD SA: #N each, as swanctl --list-sas
// shows them.
func (s *stack) sas(t *testing.T) (string, string) {
	t.Helper()
	out := s.must(t, "swanctl", "--list-sas", "--ike", files.Connection)
	ike := regexp.MustCompile(`(?m)^` + files.Connection + `: (#\d+), ESTABLISHED`).
		FindStringSubmatch(out)
	child := regexp.MustCompile(`(?m)^\s+` + files.Connection + `: (#\d+), reqid \d+, INSTALLED`).
		FindStringSubmatch(out)
	if ike == nil || child == nil {
		t.Fatalf("swanctl lists no IKE SA and CHILD SA of %s that are up:\n%s", files.Connection,
			out)
	}
	return ike[1], child[1]
}

// probe connects from a container on Docker's network to the far host's peerPort. It returns the
// address the host answers that the connection came from, and how docker run exited: nc's status,
// or Docker's own where it failed.
func (s *stack) probe(t *testing.T, network string) (string, int) {
	t.Helper()
	// An image of busybox alone, as the client has no registry to pull from.
	s.must(t, "sh", "-c", "docker image inspect probe >/dev/null 2>&1 || "+
		"docker import /opt/probe.tar probe")
	// The status goes on the last line, so that only a failure of compose's exec fails must.
	out := s.must(t, "sh", "-c", `docker run --rm --network "$1" probe /busybox nc -w 10 "$2" "$3"; `+
		`echo "$?"`, "sh", network, resolver, peerPort)
	out = strings.TrimRight(out, "\n")
	last := strings.LastIndex(out, "\n")
	status, err := strconv.Atoi(out[last+1:])
	if err != nil {
		t.Fatalf("docker run's status: %v", err)
	}
	return strings.TrimSpace(out[:max(last, 0)]), status
}

// withoutRule removes the NAT table, as nft flush ruleset would. A container on Docker's network
// must still reach the remote network from the address the server assigned. Docker's own
// MASQUERADE gives its connections the address of the link they leave by, ikeward0. One on
// forwarded no longer does, nc exiting 1, as ikeward0 drops what leaves from its own address.
func (s *stack) withoutRule(t *testing.T) {
	s.must(t, "nft", "delete", "table", "ip", routing.NATTable)
	if from, status := s.probe(t, "bridge"); status != 0 || from != s.address(t) {
		t.Errorf("with no NAT rule, a container exited %d, and reached the remote network "+
			"from %q, not from %s", status, from, s.address(t))
	}
	if from, status := s.probe(t, forwarded); status != 1 || from != "" {
		t.Errorf("with no NAT rule, a container on %s exited %d, not nc's 1: %q", forwarded,
			status, from)
	}
}

// stopLimit is how long a stop of strongSwan may take with the route in place. strongSwan waits
// for updown as it stops, which waited 10 s for strongSwan's answer before it knew to ask no more.
const stopLimit = 5 * time.Second

// strongSwanStops stops strongSwan with the route in place, and checks that updown removed the
// route without asking strongSwan, within stopLimit, and left the floor. It then starts
// strongSwan, which brings the tunnel up again and sets the route. The watchdog's timer stays
// stopped meanwhile, or its run could start strongSwan.
func (s *stack) strongSwanStops(t *testing.T) {
	s.must(t, "systemctl", "stop", install.Timer, install.Service)
	s.routed(t)
	cursor := s.cursor(t)
	// Timed in the client, without compose's exec.
	out := s.must(t, "sh", "-c", `start=$(date +%s%N); systemctl stop "$1"; `+
		`echo $((($(date +%s%N) - start) / 1000000))`, "sh", install.StrongSwan)
	ms, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("the stop's time: %v", err)
	}
	took := time.Duration(ms) * time.Millisecond
	t.Logf("systemctl stop %s took %v", install.StrongSwan, took)
	if took > stopLimit {
		t.Errorf("systemctl stop %s took %v, over %v", install.StrongSwan, took, stopLimit)
	}
	s.unrouted(t, "with strongSwan stopped")
	s.stopped(t, cursor)

	s.must(t, "systemctl", "start", install.StrongSwan)
	s.healthy(t)
	s.must(t, "systemctl", "start", install.Timer)
}

// tunnelDown takes the tunnel down, as strongSwan does when the server ends it, and checks that
// updown removed the route. close_action is none, so the tunnel stays down until the test starts
// it again, which sets the route again. The new IKE_AUTH names no IPv6 address, as charon ignores
// ikeward0.
func (s *stack) tunnelDown(t *testing.T) {
	t.Log(s.must(t, "swanctl", "--terminate", "--ike", files.Connection))
	s.unrouted(t, "with the tunnel down")
	out := s.must(t, "swanctl", "--initiate", "--child", files.Connection)
	t.Log(out)
	// All the client's IPv6 addresses but ikeward0's own are link-local, which charon never names.
	if !strings.Contains(out, "generating IKE_AUTH request") ||
		strings.Contains(out, "N(ADD_6_ADDR)") {
		t.Errorf("swanctl --initiate shows no IKE_AUTH request, or charon told the server of an "+
			"IPv6 address, as %s's own", files.Link)
	}
	s.healthy(t)
}
