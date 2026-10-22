package updown

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"net/netip"
	"strings"
	"time"

	"github.com/zadykian/ikeward/internal/status"
)

// fakeRoute is the routes through ikeward0 and the floors, which records what Run does to them. It
// fails with err, and SetFloor with floorErr.
type fakeRoute struct {
	calls         []string
	err, floorErr error
}

func (f *fakeRoute) SetRoute(dst netip.Prefix) error {
	f.calls = append(f.calls, "route "+dst.String())
	return f.err
}

func (f *fakeRoute) RemoveRoute(dst netip.Prefix) error {
	f.calls = append(f.calls, "unroute "+dst.String())
	return f.err
}

func (f *fakeRoute) SetFloor(dst netip.Prefix) error {
	f.calls = append(f.calls, "floor "+dst.String())
	return f.floorErr
}

// fakeFiles are the files under the root directory, by path. Read fails with readErr, and Write
// with writeErr.
type fakeFiles struct {
	files             map[string]string
	readErr, writeErr error
}

func (f *fakeFiles) Read(path string) ([]byte, fs.FileMode, error) {
	data, ok := f.files[path]
	switch {
	case f.readErr != nil:
		return nil, 0, f.readErr
	case !ok:
		return nil, 0, fs.ErrNotExist
	}
	return []byte(data), 0o644, nil
}

func (f *fakeFiles) Write(path string, data []byte, _ fs.FileMode) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.files == nil {
		f.files = map[string]string{}
	}
	f.files[path] = string(data)
	return nil
}

// fakeTunnel is strongSwan, which lists sas, or fails with err, and records the connections asked
// for, and the time given to answer.
type fakeTunnel struct {
	sas   []status.IKESA
	err   error
	asked []string
	given []time.Duration
}

func (f *fakeTunnel) IKESAs(ctx context.Context, conn string) ([]status.IKESA, error) {
	f.asked = append(f.asked, conn)
	if deadline, ok := ctx.Deadline(); ok {
		f.given = append(f.given, time.Until(deadline))
	}
	return f.sas, f.err
}

// fakeSystemd is systemd, which gives strongSwan's unit the ActiveState state, or fails with err,
// and records the commands it runs.
type fakeSystemd struct {
	state    string
	err      error
	commands []string
}

func (f *fakeSystemd) Run(name string, args ...string) (string, error) {
	f.commands = append(f.commands, strings.Join(append([]string{name}, args...), " "))
	return f.state + "\n", f.err
}

// running is systemd while strongSwan runs.
func running() *fakeSystemd { return &fakeSystemd{state: "active"} }

// show is how updown asks systemd whether strongSwan stops.
const show = "systemctl show --property=ActiveState --value strongswan.service"

// upSA is an IKE SA of the connection that is up with its CHILD SA, and holds vip. The server
// granted that SA ranges, as strongSwan lists them.
func upSA(vip string, ranges ...string) status.IKESA {
	return status.IKESA{State: "ESTABLISHED", VIPs: []string{vip},
		Children: []status.ChildSA{{Name: "ikeward", State: "INSTALLED", RemoteTS: ranges}}}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("the pipe is closed") }

// run runs updown in env, with route and a tunnel that lists no SA, and returns what it printed
// and its error.
func run(route *fakeRoute, env map[string]string) (string, error) {
	return runWith(route, &fakeTunnel{}, env)
}

// runWith runs updown in env, with route and tunnel, while strongSwan runs, and returns what it
// printed and its error.
func runWith(route *fakeRoute, tunnel *fakeTunnel, env map[string]string) (string, error) {
	return runIn(route, tunnel, running(), env)
}

// runIn runs updown in env, with route, tunnel and systemd, and no record, and returns what it
// printed and its error.
func runIn(route *fakeRoute, tunnel *fakeTunnel, systemd *fakeSystemd, env map[string]string) (
	string, error) {
	return runOn(route, &fakeFiles{}, tunnel, systemd, env)
}

// runOn runs updown in env, with route, files, tunnel and systemd, and returns what it printed and
// its error.
func runOn(route *fakeRoute, files *fakeFiles, tunnel *fakeTunnel, systemd *fakeSystemd,
	env map[string]string) (string, error) {
	var out strings.Builder
	err := System{Route: route, Files: files, Tunnel: tunnel, Runner: systemd,
		Env: func(key string) string { return env[key] }, Out: &out}.Run(context.Background())
	return out.String(), err
}

// The ranges the integration test's server grants: the remote network, and a second range.
const remote, second = "192.0.2.0/24", "198.18.0.0/24"

// with is env with PLUTO_PEER_CLIENT set to client.
func with(env map[string]string, client string) map[string]string {
	env = maps.Clone(env)
	env["PLUTO_PEER_CLIENT"] = client
	return env
}

// peer is the VPN server's address, which strongSwan gives in PLUTO_PEER.
const peer = "198.51.100.10"

// up and goingDown are the environments strongSwan runs updown in as the tunnel goes up, and as an
// SA goes down, for the range remote.
var (
	up = map[string]string{"PLUTO_VERB": "up-client", "PLUTO_MY_SOURCEIP": "203.0.113.130",
		"PLUTO_CONNECTION": "ikeward", "PLUTO_PEER": peer, "PLUTO_PEER_CLIENT": remote}
	goingDown = map[string]string{"PLUTO_VERB": "down-client", "PLUTO_MY_SOURCEIP": "203.0.113.130",
		"PLUTO_PEER": peer, "PLUTO_PEER_CLIENT": remote}
)

// What the fake records as Run sets and removes the route to remote, and makes its floor.
const (
	set   = "route " + remote
	unset = "unroute " + remote
	floor = "floor " + remote
)

// The lines Run prints of the route to remote as it sets it, records it, keeps it for another SA,
// and removes it.
const (
	routed  = "Routed " + remote + " through ikeward0 in table 220, its floor at metric 4000 below\n"
	learned = "Learned " + remote + ", which /var/lib/ikeward/learned-ranges records\n"
	kept    = "Kept the route to " + remote + " through ikeward0 in table 220, as another SA " +
		"grants it\n"
	unrouted = "Removed the route to " + remote + " through ikeward0 from table 220"
)
