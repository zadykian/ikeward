// Package updown is ikeward updown, which strongSwan runs as the tunnel goes up or down, once for
// each range the VPN server granted, which PLUTO_PEER_CLIENT names. As the tunnel goes up,
// it routes the range through files.Link, the tunnel's link, in files.Table, and makes its floor
// there. It records the range in routing.Learned too, for ikeward routing's floors from boot on.
// It refuses a range wider than /8, or one that holds the VPN server's address, as routing.Refusal
// says, and leaves the tunnel up for the others. As the tunnel goes down, it removes the route
// again, and the floor stays. charon routes nothing there for SAs with an interface ID. The kernel
// removes the routes too as the address the server assigned leaves the link. ikeward routing's
// rules look the table up after the server's own networks. The package reaches the routes and
// floors through Route, the record through Files, strongSwan through status.Tunnel, and systemd
// through Runner, which unit tests replace with fakes.
package updown

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// The verbs strongSwan gives in PLUTO_VERB as an IPv4 CHILD SA goes up and down whose own side is
// an address from the server.
const (
	Up   = "up-client"
	Down = "down-client"
)

// Route is the kernel's routes through files.Link in files.Table, and the floors there.
type Route interface {
	// SetRoute routes dst through files.Link in files.Table, in place of any route there to dst of
	// metric 0.
	SetRoute(dst netip.Prefix) error
	// RemoveRoute removes the route to dst through files.Link from files.Table, where it exists.
	RemoveRoute(dst netip.Prefix) error
	// SetFloor makes dst unreachable in files.Table at routing.FloorMetric, in place of any route
	// there to dst of that metric.
	SetFloor(dst netip.Prefix) error
}

// Runner runs commands.
type Runner interface {
	// Run runs the program name with args and no input, and returns what it printed on stdout.
	Run(name string, args ...string) (string, error)
}

// System is what updown reads and changes.
type System struct {
	// Route is the routes through the tunnel, and the floors. The kernel removes the routes as the
	// address the server assigned leaves files.Link, so updown sets them each time the tunnel goes
	// up.
	Route Route
	// Files hold routing.Learned.
	Files routing.Files
	// Tunnel is strongSwan, which must list its IKE SAs without waiting for the one it runs
	// updown for.
	Tunnel status.Tunnel
	// Runner runs systemctl, which says whether strongSwan stops.
	Runner Runner
	// Env is the environment strongSwan runs updown in: os.Getenv on a live system.
	Env func(key string) string
	// Out is where it says what it did, which strongSwan logs.
	Out io.Writer
}

// askTimeout bounds the wait for strongSwan's answer, which a charon that runs answers at once,
// as updown asks with noblock. strongSwan waits for updown, so one that does not answer holds up
// no more than this.
const askTimeout = 3 * time.Second

// deactivating is the ActiveState systemd gives a unit while it stops it, to stop or to restart it.
const deactivating = "deactivating"

// Run, where PLUTO_VERB is Up, learns the range PLUTO_PEER_CLIENT names, unless it refuses it.
// For Down, it removes the route, unless another SA grants the range: it keeps the route then, for
// that SA. Where strongSwan cannot say, the route stays, and Run fails. As strongSwan stops, it
// removes the route unasked.
func (s System) Run(ctx context.Context) error {
	switch verb := s.Env("PLUTO_VERB"); verb {
	case Up, Down:
		dst, peer, err := s.granted()
		if err != nil {
			return err
		}
		if why := routing.Refusal(dst, []netip.Addr{peer}); why != "" {
			return s.refuse(verb, dst, peer, why)
		}
		if verb == Up {
			return s.learn(dst, peer)
		}
		return s.down(ctx, dst)
	case "":
		// Run fails without a verb, which strongSwan always gives.
		return errors.New("PLUTO_VERB is not set: strongSwan runs updown as the tunnel goes up " +
			"or down, and says which in it")
	default:
		// Run passes over any other verb: an IPv6 SA's, or one with no address from the server.
		return s.printf("Nothing to do for PLUTO_VERB=%s: the route is for an IPv4 CHILD SA "+
			"with an address from the server\n", verb)
	}
}

// granted is the range PLUTO_PEER_CLIENT names, a subnet the VPN server granted, and the
// VPN server's address, PLUTO_PEER. strongSwan gives an IPv4 range with the verbs Up and Down: for
// a range that is no subnet, the least that holds it, as status.Ranges reads the SA's.
func (s System) granted() (netip.Prefix, netip.Addr, error) {
	client := s.Env("PLUTO_PEER_CLIENT")
	dst, err := netip.ParsePrefix(client)
	if err != nil || !dst.Addr().Is4() {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("PLUTO_PEER_CLIENT is %q, no IPv4 "+
			"range: strongSwan names there the range the server granted", client)
	}
	peer, err := netip.ParseAddr(s.Env("PLUTO_PEER"))
	if err != nil {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("PLUTO_PEER is %q, no address: "+
			"strongSwan names there the VPN server's", s.Env("PLUTO_PEER"))
	}
	return dst.Masked(), peer.Unmap(), nil
}

// refuse passes over dst, which routing.Refusal refuses for why: no route, floor or record, and
// no error, so that the SA stays up for the ranges updown takes. As the tunnel goes up, it records
// peer, so that ikeward routing passes over such a range too, where one is written by hand.
func (s System) refuse(verb string, dst netip.Prefix, peer netip.Addr, why string) error {
	var recordErr error
	if verb == Up {
		if _, err := routing.Learn(s.Files, peer, netip.Prefix{}); err != nil {
			recordErr = fmt.Errorf("cannot record %s: %w", peer, err)
		}
	}
	err := s.printf("Refused %s, which the server granted: %s\n", dst, why)
	return errors.Join(err, recordErr)
}

// learn routes dst, makes its floor and records it, with peer, then says so. The route comes
// first, as the tunnel carries nothing for dst without it. The floor and the record keep dst's
// traffic off the default route once the tunnel goes down, until boot and after.
func (s System) learn(dst netip.Prefix, peer netip.Addr) error {
	if err := s.Route.SetRoute(dst); err != nil {
		return fmt.Errorf("cannot route %s through %s in table %d: %w", dst, files.Link,
			files.Table, err)
	}
	var floorErr error
	if err := s.Route.SetFloor(dst); err != nil {
		floorErr = fmt.Errorf("cannot make %s unreachable in table %d: %w", dst, files.Table, err)
	}
	added, recordErr := routing.Learn(s.Files, peer, dst)
	if recordErr != nil {
		recordErr = fmt.Errorf("cannot record %s: %w", dst, recordErr)
	}
	routed := fmt.Sprintf("Routed %s through %s in table %d", dst, files.Link, files.Table)
	if floorErr == nil {
		routed += fmt.Sprintf(", its floor at metric %d below", routing.FloorMetric)
	}
	err := s.printf("%s\n", routed)
	if added && err == nil {
		err = s.printf("Learned %s, which %s records\n", dst, routing.Learned)
	}
	return errors.Join(err, floorErr, recordErr)
}

// down removes the route to dst, unless another IKE SA of the connection is up with a CHILD SA
// that grants dst: then it sets the route again. strongSwan brings up the new SAs of a
// reauthentication before it deletes the old ones, so the old SA's Down comes after the new one's
// Up. Where strongSwan cannot say, the route stays, and down fails.
func (s System) down(ctx context.Context, dst netip.Prefix) error {
	// As systemd stops or restarts strongSwan, charon runs down for each SA it closes, but no
	// longer answers VICI, and no SA stays up: down removes the route without asking.
	stopping, stateErr := s.stopping()
	if stopping {
		return s.remove(dst, ", as strongSwan stops")
	}
	// Otherwise it asks, for askTimeout at most. A route left with the tunnel down takes no
	// traffic that could reach the remote network, as files.Link sends nothing without an SA. Removed
	// with the tunnel up, it cuts off the server, containers and forwarded networks.
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	sas, err := s.Tunnel.IKESAs(ctx, files.Connection)
	if err != nil {
		// The cause, and systemd's part, take a line each, so that each fits in one of
		// strongSwan's reads.
		err = fmt.Errorf("cannot ask strongSwan whether another SA grants %s, so its route "+
			"stays:\n  %w", dst, err)
		if stateErr != nil {
			err = fmt.Errorf("%w\n  nor can systemd say whether it stops: %w", err, stateErr)
		}
		return err
	}
	if status.Grants(sas, dst) {
		return s.keep(dst)
	}
	return s.remove(dst, "")
}

// stopping is whether systemd is stopping strongSwan's unit, to stop or to restart it. It also
// returns systemctl's error where systemctl cannot say, as where systemd does not run strongSwan.
func (s System) stopping() (bool, error) {
	state, err := s.Runner.Run("systemctl", "show", "--property=ActiveState", "--value",
		files.StrongSwan)
	if err != nil {
		return false, fmt.Errorf("systemctl show %s: %w", files.StrongSwan, err)
	}
	return strings.TrimSpace(state) == deactivating, nil
}

// keep sets the route to dst again, for another SA that grants it, then says so. strongSwan logs
// what updown prints in reads of 127 bytes at most, so each line fits in one.
func (s System) keep(dst netip.Prefix) error {
	if err := s.Route.SetRoute(dst); err != nil {
		return fmt.Errorf("cannot route %s through %s in table %d: %w", dst, files.Link,
			files.Table, err)
	}
	return s.printf("Kept the route to %s through %s in table %d, as another SA grants it\n", dst,
		files.Link, files.Table)
}

// remove removes the route to dst, then says so, ended by why. dst's floor stays.
func (s System) remove(dst netip.Prefix, why string) error {
	if err := s.Route.RemoveRoute(dst); err != nil {
		return fmt.Errorf("cannot remove the route to %s through %s from table %d: %w", dst,
			files.Link, files.Table, err)
	}
	return s.printf("Removed the route to %s through %s from table %d%s\n", dst, files.Link,
		files.Table, why)
}

// printf writes to Out.
func (s System) printf(format string, args ...any) error {
	_, err := fmt.Fprintf(s.Out, format, args...)
	return err
}
