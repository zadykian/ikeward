package main

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/routing"
)

// fakeRoute is the routes through the tunnel's link and the floors, which records what updown
// does to them, and fails with err.
type fakeRoute struct {
	calls []string
	err   error
}

// SetRoute records that updown routed dst through the tunnel's link.
func (f *fakeRoute) SetRoute(dst netip.Prefix) error {
	f.calls = append(f.calls, "route "+dst.String())
	return f.err
}

// RemoveRoute records that updown removed the route to dst.
func (f *fakeRoute) RemoveRoute(dst netip.Prefix) error {
	f.calls = append(f.calls, "unroute "+dst.String())
	return f.err
}

// SetFloor records that updown made dst unreachable below its route.
func (f *fakeRoute) SetFloor(dst netip.Prefix) error {
	f.calls = append(f.calls, "floor "+dst.String())
	return f.err
}

// down is the verb strongSwan runs updown with as an SA goes down.
const down = "down-client"

// What the fake records as updown sets the route, makes the floor and removes the route, and what
// updown prints of each. The range is the one a live server's VPN server grants.
const (
	route    = "route 10.88.0.0/16"
	floor    = "floor 10.88.0.0/16"
	unroute  = "unroute 10.88.0.0/16"
	routed   = "Routed 10.88.0.0/16 through ikeward0 in table 220, its floor at metric 4000 below\n"
	learned  = "Learned 10.88.0.0/16, which /var/lib/ikeward/learned-ranges records\n"
	unrouted = "Removed the route to 10.88.0.0/16 through ikeward0 from table 220"
)

// unitState is systemd, which gives strongSwan's unit its ActiveState.
type unitState string

func (s unitState) Run(string, ...string) (string, error) { return string(s) + "\n", nil }

// updownSystem is a system as root, whose route is r and files f, run by strongSwan with verb for
// 10.88.0.0/16, which the server granted. strongSwan runs, and no other SA of the connection is
// up.
func updownSystem(r *fakeRoute, f memFiles, verb string, out *strings.Builder) system {
	env := map[string]string{"PLUTO_VERB": verb, "PLUTO_MY_SOURCEIP": vip.String(),
		"PLUTO_PEER": "198.51.100.10", "PLUTO_PEER_CLIENT": "10.88.0.0/16"}
	return system{stdout: out, route: r, files: f, updownTunnel: fakeTunnel{},
		runner: unitState("active"), env: func(key string) string { return env[key] },
		euid: func() int { return 0 }}
}

// updown sets the route as the tunnel goes up, with its floor, and records the range. It removes
// the route as the tunnel goes down, saying so on standard output, which strongSwan logs.
func TestUpdownCommand(t *testing.T) {
	r, f := &fakeRoute{}, memFiles{}
	var out strings.Builder
	if err := run([]string{"updown"}, updownSystem(r, f, "up-client", &out)); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"updown"}, updownSystem(r, f, down, &out)); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), routed+learned+unrouted+"\n"; got != want {
		t.Errorf("printed:\n%s\nwant:\n%s", got, want)
	}
	if want := []string{route, floor, unroute}; !slices.Equal(r.calls, want) {
		t.Errorf("did %q, want %q", r.calls, want)
	}
	if got := f[routing.Learned]; !strings.HasSuffix(got, "\n10.88.0.0/16\n") {
		t.Errorf("recorded %q", got)
	}
}

// As the old SA of a reauthentication goes down while the new one is up and grants the range,
// updown keeps the route.
func TestUpdownKeeps(t *testing.T) {
	r := &fakeRoute{}
	var out strings.Builder
	sys := updownSystem(r, memFiles{}, down, &out)
	sys.updownTunnel = up
	if err := run([]string{"updown"}, sys); err != nil {
		t.Fatal(err)
	}
	want := "Kept the route to 10.88.0.0/16 through ikeward0 in table 220, as another SA grants " +
		"it\n"
	if out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
	if want := []string{route}; !slices.Equal(r.calls, want) {
		t.Errorf("did %q, want %q", r.calls, want)
	}
}

// As systemd stops strongSwan, updown removes the route without asking strongSwan, which no
// longer answers.
func TestUpdownStopping(t *testing.T) {
	r := &fakeRoute{}
	var out strings.Builder
	sys := updownSystem(r, memFiles{}, down, &out)
	sys.runner = unitState("deactivating")
	sys.updownTunnel = fakeTunnel{err: errors.New("context deadline exceeded")}
	if err := run([]string{"updown"}, sys); err != nil {
		t.Fatal(err)
	}
	if want := unrouted + ", as strongSwan stops\n"; out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
	if want := []string{unroute}; !slices.Equal(r.calls, want) {
		t.Errorf("did %q, want %q", r.calls, want)
	}
}

// A grant of every address is refused: updown says so, and exits 0, routing nothing, so that
// strongSwan keeps the SA up for the ranges it takes.
func TestUpdownRefuses(t *testing.T) {
	r, f := &fakeRoute{}, memFiles{}
	var out strings.Builder
	sys := updownSystem(r, f, "up-client", &out)
	sys.env = func(key string) string {
		return map[string]string{"PLUTO_VERB": "up-client", "PLUTO_PEER": "198.51.100.10",
			"PLUTO_PEER_CLIENT": "0.0.0.0/0"}[key]
	}
	if err := run([]string{"updown"}, sys); err != nil {
		t.Fatal(err)
	}
	want := "Refused 0.0.0.0/0, which the server granted: wider than /8, it would take the " +
		"server's own traffic\n"
	if out.String() != want || len(r.calls) > 0 || strings.Contains(f[routing.Learned],
		"0.0.0.0/0\n") {
		t.Errorf("printed %q, did %q, and recorded %q; want %q", out.String(), r.calls,
			f[routing.Learned], want)
	}
}

// updown runs as root alone; it exits 1 where it fails, which strongSwan logs.
func TestUpdownFails(t *testing.T) {
	for _, test := range []struct {
		name  string
		setUp func(*system, *fakeRoute)
		want  string
		calls int
	}{
		{"not root", func(s *system, _ *fakeRoute) { s.euid = func() int { return 1000 } },
			"updown changes the routes, and must run as root", 0},
		{"by hand", func(s *system, _ *fakeRoute) { s.env = func(string) string { return "" } },
			"PLUTO_VERB is not set: strongSwan runs updown as the tunnel goes up or down, and " +
				"says which in it", 0},
		{"netlink", func(_ *system, r *fakeRoute) { r.err = errors.New("operation not permitted") },
			"cannot route 10.88.0.0/16 through ikeward0 in table 220: operation not permitted", 1},
		{"no range", func(s *system, _ *fakeRoute) {
			s.env = func(key string) string { return map[string]string{"PLUTO_VERB": down}[key] }
		}, `PLUTO_PEER_CLIENT is "", no IPv4 range: strongSwan names there the range the server ` +
			"granted", 0},
		{"no answer", func(s *system, _ *fakeRoute) {
			s.env = func(key string) string {
				return map[string]string{"PLUTO_VERB": down, "PLUTO_PEER": "198.51.100.10",
					"PLUTO_PEER_CLIENT": "10.0.0.0/8"}[key]
			}
			s.updownTunnel = fakeTunnel{err: errors.New("context deadline exceeded")}
		}, "cannot ask strongSwan whether another SA grants 10.0.0.0/8, so its route stays:\n" +
			"  context deadline exceeded", 0},
	} {
		r := &fakeRoute{}
		var out strings.Builder
		sys := updownSystem(r, memFiles{}, "up-client", &out)
		test.setUp(&sys, r)
		err := run([]string{"updown"}, sys)
		if err == nil || err.Error() != test.want || exitStatus(err) != 1 {
			t.Errorf("%s: %v, want %s, and exit status 1", test.name, err, test.want)
		}
		if out.Len() > 0 || len(r.calls) != test.calls {
			t.Errorf("%s: printed %q, and did %q", test.name, out.String(), r.calls)
		}
	}
}
