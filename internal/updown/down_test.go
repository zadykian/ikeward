package updown

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/status"
)

// As systemd stops or restarts strongSwan, charon runs down-client for each SA it closes, but no
// longer answers VICI. Run removes the route without asking it, as no SA stays up, and says why.
func TestDownStopping(t *testing.T) {
	route := &fakeRoute{}
	// strongSwan would list an SA up, and fail, were it asked.
	tunnel := &fakeTunnel{sas: []status.IKESA{upSA("203.0.113.131", remote)},
		err: context.DeadlineExceeded}
	systemd := &fakeSystemd{state: "deactivating"}
	for range 2 {
		out, err := runIn(route, tunnel, systemd, goingDown)
		if want := unrouted + ", as strongSwan stops\n"; err != nil || out != want {
			t.Errorf("%q, %v; want %q", out, err, want)
		}
	}
	if want := []string{unset, unset}; !slices.Equal(route.calls, want) {
		t.Errorf("did %q, want %q", route.calls, want)
	}
	if want := []string{show, show}; !slices.Equal(systemd.commands, want) {
		t.Errorf("ran %q, want %q", systemd.commands, want)
	}
	if len(tunnel.asked) > 0 {
		t.Errorf("asked strongSwan for %q", tunnel.asked)
	}
}

// Only a unit that stops spares strongSwan the question; where systemctl cannot say, as where
// systemd does not run strongSwan, Run asks it, as when it runs.
func TestDownAsks(t *testing.T) {
	for name, systemd := range map[string]*fakeSystemd{
		"activating": {state: "activating"},
		"reloading":  {state: "reloading"},
		"inactive":   {state: "inactive"},
		"no systemd": {err: errors.New("exit status 1: System has not been booted with systemd")},
	} {
		route := &fakeRoute{}
		tunnel := &fakeTunnel{sas: []status.IKESA{upSA("203.0.113.131", remote)}}
		out, err := runIn(route, tunnel, systemd, goingDown)
		if err != nil || out != kept || len(tunnel.asked) != 1 {
			t.Errorf("%s: %q, %v, asked %d times; want %q", name, out, err, len(tunnel.asked),
				kept)
		}
	}
}

// In a reauthentication, strongSwan brings up the new SA before it deletes the old one, as on
// strongSwan 6.0.4 with make_before_break. The old SA's down-client comes last for each range,
// while the new SA is up. It sets the route again where the new SA grants the range, as the old
// address's removal took it, and removes it where the server no longer grants it.
func TestOverlap(t *testing.T) {
	route := &fakeRoute{}
	newer := &fakeTunnel{sas: []status.IKESA{upSA("203.0.113.131", "10.0.0.1..10.0.0.5",
		remote+"[tcp/80]")}}
	var out strings.Builder
	for _, test := range []struct {
		tunnel *fakeTunnel
		env    map[string]string
	}{
		{&fakeTunnel{}, with(up, "10.0.0.0/29")},
		{&fakeTunnel{}, up},
		{newer, with(goingDown, "10.0.0.0/29")},
		{newer, goingDown},
		{newer, goingDown},
		{newer, with(goingDown, second)},
	} {
		printed, err := runWith(route, test.tunnel, test.env)
		if err != nil {
			t.Fatal(err)
		}
		out.WriteString(printed)
	}
	ten := func(line string) string { return strings.ReplaceAll(line, remote, "10.0.0.0/29") }
	if want := ten(routed) + ten(learned) + routed + learned + ten(kept) + strings.Repeat(kept, 2) +
		strings.ReplaceAll(unrouted, remote, second) + "\n"; out.String() != want {
		t.Errorf("printed:\n%s\nwant:\n%s", out.String(), want)
	}
	if want := []string{"route 10.0.0.0/29", "floor 10.0.0.0/29", set, floor, "route 10.0.0.0/29",
		set, set, "unroute " + second}; !slices.Equal(route.calls, want) {
		t.Errorf("did %q, want %q", route.calls, want)
	}
}

// Only an IKE SA that is up, whose CHILD SA of the connection is up too and grants the range,
// keeps the route.
func TestDownRemoves(t *testing.T) {
	deleting := upSA("203.0.113.131", remote)
	deleting.State = "DELETING"
	childDeleted := upSA("203.0.113.131", remote)
	childDeleted.Children[0].State = "DELETED"
	otherChild := upSA("203.0.113.131", remote)
	otherChild.Children[0].Name = "other"
	for name, sas := range map[string][]status.IKESA{
		"none":          nil,
		"connecting":    {{State: "CONNECTING"}},
		"IKE deleting":  {deleting},
		"child deleted": {childDeleted},
		"other child":   {otherChild},
		"other ranges":  {upSA("203.0.113.131", second, "192.0.2.0/25", "192.0.2.0/23")},
	} {
		route := &fakeRoute{}
		out, err := runWith(route, &fakeTunnel{sas: sas}, goingDown)
		if want := unrouted + "\n"; err != nil || out != want ||
			!slices.Equal(route.calls, []string{unset}) {
			t.Errorf("%s: %q, %v, and did %q; want %q", name, out, err, route.calls, want)
		}
	}
}

// Where down-client cannot keep the route for the SA that is up, it fails, and says nothing else.
func TestKeepFails(t *testing.T) {
	route := &fakeRoute{err: errors.New("operation not permitted")}
	out, err := runWith(route, &fakeTunnel{sas: []status.IKESA{upSA("203.0.113.130", remote)}},
		goingDown)
	want := "cannot route " + remote + " through ikeward0 in table 220: operation not permitted"
	if err == nil || err.Error() != want || out != "" || !slices.Equal(route.calls,
		[]string{set}) {
		t.Errorf("%q, %v, and did %q; want %s", out, err, route.calls, want)
	}
}

// Where strongSwan runs but cannot say whether another SA grants the range, down-client leaves the
// route alone, and fails. Left while the tunnel is down, it takes no traffic that could reach the
// remote network. Where systemctl could not say whether strongSwan stops, the error says so too.
func TestDownTunnelFails(t *testing.T) {
	const asked = "cannot ask strongSwan whether another SA grants " + remote + ", so its route " +
		"stays:\n  "
	for _, test := range []struct {
		name     string
		systemd  *fakeSystemd
		tunnel   error
		want     string
		deadline bool
	}{
		{"refused", running(), errors.New("/run/charon.vici: connection refused"),
			asked + "/run/charon.vici: connection refused", false},
		{"no answer", running(), context.DeadlineExceeded, asked + "context deadline exceeded",
			true},
		{"no systemd either", &fakeSystemd{err: errors.New("exit status 1: Failed to connect " +
			"to bus")}, errors.New("/run/charon.vici: no such file or directory"), asked +
			"/run/charon.vici: no such file or directory\n  nor can systemd say whether it " +
			"stops: systemctl show strongswan.service: exit status 1: Failed to connect to bus",
			false},
	} {
		route := &fakeRoute{}
		out, err := runIn(route, &fakeTunnel{err: test.tunnel}, test.systemd, goingDown)
		if err == nil || err.Error() != test.want || out != "" || len(route.calls) > 0 {
			t.Errorf("%s: %q, %v, and did %q; want %s", test.name, out, err, route.calls,
				test.want)
		}
		if test.deadline && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: %v does not wrap the deadline", test.name, err)
		}
	}
}

// Where the route cannot go as strongSwan stops, down-client fails, and says nothing else.
func TestDownStoppingFails(t *testing.T) {
	route := &fakeRoute{err: errors.New("operation not permitted")}
	tunnel := &fakeTunnel{}
	out, err := runIn(route, tunnel, &fakeSystemd{state: "deactivating"}, goingDown)
	want := "cannot remove the route to " + remote + " through ikeward0 from table 220: " +
		"operation not permitted"
	if err == nil || err.Error() != want || out != "" || len(tunnel.asked) > 0 ||
		!slices.Equal(route.calls, []string{unset}) {
		t.Errorf("%q, %v, and did %q; want %s", out, err, route.calls, want)
	}
}
