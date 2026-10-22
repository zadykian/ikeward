package integration

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
)

const (
	// router is the VPN server's address on the Internet. It stands in for the uplink's router
	// too, which takes what the client sends in the clear on to the remote network. The far
	// host answers through it. uplink is the client's own address there.
	router, uplink = "198.51.100.10", "198.51.100.20"
	// lanHost is the LAN host's address, inside given, 10.88.0.0/16, and onLAN the client's own.
	lanHost, onLAN = "10.88.88.208", "10.88.88.50"
	// ungranted is an address inside given that the server grants none of. given's
	// floor makes it unreachable, the tunnel up or down.
	ungranted = "10.88.42.10"
	// unreachable is how busybox's nc fails to connect to a host a floor makes unreachable, as
	// connect() fails with EHOSTUNREACH, or the client answers a container's SYN so.
	unreachable = "No route to host"
	// counted counts the packets to the remote ranges, and to ungranted, that leave the client by
	// any link, in a table of nftables of the test's own. A packet a floor stops never gets there.
	counted = "table ip leaks {\n chain postrouting {\n" +
		"  type filter hook postrouting priority 300; policy accept;\n" +
		"  ip daddr { " + remoteRange + ", " + secondRange + ", " + ungranted +
		" } counter\n }\n}\n"
)

// remoteHosts are the far host's addresses, one in each range the server grants.
var remoteHosts = []string{resolver, secondHost}

// ownFirst checks the order with the tunnel up. The client and a container reach the LAN host
// from the client's address on the LAN, and the LAN host reaches the client. The client, a
// container and a forwarded network reach the remote host in each range through the tunnel,
// from the server's address, which the client pings. given's floor stops ungranted.
func (s *stack) ownFirst(t *testing.T) {
	s.must(t, "systemctl", "is-active", "--quiet", routing.Unit)
	s.healthy(t)
	address := s.address(t)
	s.lanReached(t, "with the tunnel up")
	for _, from := range []string{"", "bridge", forwarded} {
		for _, host := range remoteHosts {
			if r := s.reach(t, from, host); !r.pinged || r.peer != address {
				t.Errorf("from %s, ping passed: %v, and TCP reached %s from %q, not from %s: %s",
					where(from), r.pinged, host, r.peer, address, r.err)
			}
		}
	}
	if r := s.reach(t, "", ungranted); r.pinged || r.peer != "" || !strings.Contains(r.err,
		unreachable) {
		t.Errorf("ping of %s passed: %v, and TCP reached it from %q, or failed otherwise than %q: "+
			"%s", ungranted, r.pinged, r.peer, unreachable, r.err)
	}
	if _, err := s.run(t, "", "busybox", "ping", "-c", "2", "-W", "2", address); err != nil {
		t.Errorf("the client's ping of its own address %s failed: %v", address, err)
	}
	t.Log(s.must(t, "ip", "rule"))
	t.Log(s.must(t, "ip", "route", "show", "table", "220"))
}

// lanReached checks that the client and a container reach the LAN host from the client's address
// on the LAN. The LAN host must reach the client too, by ping and by TCP. when says when.
func (s *stack) lanReached(t *testing.T, when string) {
	t.Helper()
	for _, from := range []string{"", "bridge"} {
		if r := s.reach(t, from, lanHost); !r.pinged || r.peer != onLAN {
			t.Errorf("%s, from %s, ping passed: %v, and TCP reached the LAN host from %q, not "+
				"from %s: %s", when, where(from), r.pinged, r.peer, onLAN, r.err)
		}
	}
	// systemd answers each connection to port 2222 with the word client.
	s.must(t, "systemd-run", "--unit=lan-listener", "--collect", "systemd-socket-activate",
		"--listen=2222", "--accept", "--inetd", "/bin/echo", "client")
	defer s.must(t, "systemctl", "stop", "lan-listener")
	ctx, cancel := context.WithTimeout(s.ctx, commandTimeout)
	defer cancel()
	out, err := s.compose(ctx, "", "exec", "-T", "lan", "sh", "-c", "ping -c 2 -W 2 "+onLAN+
		" >/dev/null && echo pinged; socat -t 3 -T 3 - TCP:"+onLAN+":2222,connect-timeout=3")
	if err != nil || out != "pinged\nclient\n" {
		t.Errorf("%s, the LAN host reached the client: %q, %v", when, out, err)
	}
}

// floorDown stops strongSwan, and gives the client a default route through router, as a server's
// uplink has. The same packets, and those to ungranted, must not leave, and the LAN host stays in
// reach. Without the floors, the far host answers them by the uplink, until the routing's unit
// makes the floors again, from the record of the learned ranges. strongSwan then starts again.
func (s *stack) floorDown(t *testing.T) {
	s.must(t, "systemctl", "stop", install.Timer, install.Service, install.StrongSwan)
	s.unrouted(t, "with strongSwan stopped")
	s.must(t, "ip", "route", "replace", "default", "via", router)
	if out, err := s.run(t, counted, "nft", "-f", "-"); err != nil {
		t.Fatalf("nft -f: %v\n%s", err, out)
	}
	// The kernel sends a container's address 6 ICMP errors in a burst, then one a second. So the
	// sources take turns, each getting one for each packet, and the client alone tries ungranted.
	for _, host := range append(slices.Clone(remoteHosts), ungranted) {
		for _, from := range []string{"", "bridge", forwarded} {
			if host == ungranted && from != "" {
				continue
			}
			r := s.reach(t, from, host)
			if r.pinged || r.peer != "" || !strings.Contains(r.err, unreachable) {
				t.Errorf("with strongSwan stopped, from %s, ping passed: %v, and TCP reached %s "+
					"from %q, or failed otherwise than %q: %s", where(from), r.pinged, host, r.peer,
					unreachable, r.err)
			}
		}
	}
	if left := s.leaks(t); left != 0 {
		t.Errorf("with strongSwan stopped, %d packets for the remote ranges left the client",
			left)
	}
	s.lanReached(t, "with strongSwan stopped")
	s.withoutFloors(t)
	s.must(t, "systemctl", "restart", routing.Unit)
	s.unrouted(t, "with the floors made again")
	for _, host := range remoteHosts {
		if r := s.reach(t, "", host); r.pinged || r.peer != "" {
			t.Errorf("with the floors made again, ping passed: %v, and TCP reached %s from %q",
				r.pinged, host, r.peer)
		}
	}

	s.must(t, "nft", "delete", "table", "ip", "leaks")
	s.must(t, "ip", "route", "delete", "default", "via", router)
	s.must(t, "systemctl", "start", install.StrongSwan)
	s.healthy(t)
	s.must(t, "systemctl", "start", install.Timer)
}

// withoutFloors removes the floors, with strongSwan stopped, and checks that the packets then
// leave in the clear, and the far host answers them. Each container pings from the same address
// with the same ID, so conntrack may keep the flow's address from before, and a container's ping
// then gets no answer.
func (s *stack) withoutFloors(t *testing.T) {
	t.Helper()
	s.must(t, "ip", "route", "flush", "table", "220")
	for _, from := range []string{"", "bridge", forwarded} {
		for _, host := range remoteHosts {
			if r := s.reach(t, from, host); (from == "" && !r.pinged) || r.peer != uplink {
				t.Errorf("without the floors, from %s, ping passed: %v, and TCP reached %s from "+
					"%q, not from the uplink's %s: %s", where(from), r.pinged, host, r.peer,
					uplink, r.err)
			}
		}
	}
	t.Logf("without the floors, %d packets for the remote ranges left the client", s.leaks(t))
}

// leaks is how many packets for the remote ranges have left the client, as counted counts them.
func (s *stack) leaks(t *testing.T) int {
	t.Helper()
	out := s.must(t, "nft", "list", "chain", "ip", "leaks", "postrouting")
	m := regexp.MustCompile(`counter packets (\d+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("nftables shows no counter:\n%s", out)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// where names the source of reach's packets: the client, or a container on Docker's network.
func where(network string) string {
	if network == "" {
		return "the client"
	}
	return "a container on Docker's network " + network
}

// reached is what reach found: whether the ping passed, the address the host answered that the
// connection came from, and nc's error where none came.
type reached struct {
	pinged    bool
	peer, err string
}

// reach pings host, then connects to its peerPort, from the client where network is empty, else
// from a container on Docker's network.
func (s *stack) reach(t *testing.T, network, host string) reached {
	t.Helper()
	script := `"$0" ping -c 2 -W 2 "$1" >/dev/null 2>&1; echo "ping=$?"; ` +
		`echo "tcp=$("$0" nc -w 3 "$1" "$2" 2>&1 | "$0" tr '\n' ' ')"`
	command := []string{"sh", "-c", script, "busybox", host, peerPort}
	if network != "" {
		s.must(t, "sh", "-c", "docker image inspect probe >/dev/null 2>&1 || "+
			"docker import /opt/probe.tar probe")
		command = []string{"docker", "run", "--rm", "--cap-add", "NET_RAW", "--network", network,
			"probe", "/busybox", "sh", "-c", script, "/busybox", host, peerPort}
	}
	out := s.must(t, command...)
	ping := regexp.MustCompile(`(?m)^ping=(\d+)$`).FindStringSubmatch(out)
	tcp := regexp.MustCompile(`(?m)^tcp=(.*)$`).FindStringSubmatch(out)
	if ping == nil || tcp == nil {
		t.Fatalf("reach from %s printed %q", where(network), out)
	}
	r := reached{pinged: ping[1] == "0"}
	if answer := strings.TrimSpace(tcp[1]); strings.HasPrefix(answer, "nc: ") {
		r.err = answer
	} else {
		r.peer = answer
	}
	return r
}
