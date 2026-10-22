package updown

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// As the tunnel goes up, Run routes each range the server granted, makes its floor, and records it
// once. As it goes down, Run asks systemd whether strongSwan stops and, as it does not, asks
// strongSwan for the connection's SAs, for askTimeout at most. Where none grants the range, it
// removes the route, and the floor stays. Each says so, and does the same where it runs again.
func TestRun(t *testing.T) {
	route := &fakeRoute{}
	files := &fakeFiles{}
	tunnel := &fakeTunnel{}
	systemd := running()
	var out strings.Builder
	for _, env := range []map[string]string{up, with(up, second), up, with(up, "198.18.0.7/24")} {
		printed, err := runOn(route, files, tunnel, systemd, env)
		if err != nil {
			t.Fatal(err)
		}
		out.WriteString(printed)
	}
	for range 2 {
		printed, err := runOn(route, files, tunnel, systemd, goingDown)
		if want := unrouted + "\n"; err != nil || printed != want {
			t.Errorf("down: %q, %v; want %q", printed, err, want)
		}
	}
	routedSecond := strings.ReplaceAll(routed, remote, second)
	if want := routed + learned + routedSecond + strings.ReplaceAll(learned, remote, second) +
		routed + routedSecond; out.String() != want {
		t.Errorf("up printed:\n%s\nwant:\n%s", out.String(), want)
	}
	if want := []string{set, floor, "route " + second, "floor " + second, set, floor,
		"route " + second, "floor " + second, unset, unset}; !slices.Equal(route.calls, want) {
		t.Errorf("did %q, want %q", route.calls, want)
	}
	if got := files.files[routing.Learned]; !strings.HasSuffix(got, "\n"+remote+"\n"+second+"\n") {
		t.Errorf("recorded %q, want %s and %s", got, remote, second)
	}
	if want := []string{show, show}; !slices.Equal(systemd.commands, want) {
		t.Errorf("ran %q, want %q", systemd.commands, want)
	}
	if want := []string{"ikeward", "ikeward"}; !slices.Equal(tunnel.asked, want) {
		t.Errorf("asked for %q, want %q", tunnel.asked, want)
	}
	for _, given := range tunnel.given {
		if given <= 0 || given > askTimeout {
			t.Errorf("gave strongSwan %v to answer, want %v at most", given, askTimeout)
		}
	}
	if len(tunnel.given) != 2 {
		t.Errorf("gave strongSwan no deadline: %v", tunnel.given)
	}
}

// A grant wider than /8, or one that holds the VPN server's address, is refused. It takes no
// route, floor or record, and no error, so that the SA stays up for the other ranges. The record
// keeps the server's address, for routing's floors. A /8, and a range beside the server's, are
// learned.
func TestRefused(t *testing.T) {
	const wide = "wider than /8, it would take the server's own traffic"
	const holds = "it holds 198.51.100.10, the VPN server's address"
	for _, test := range []struct{ client, why string }{
		{"0.0.0.0/0", wide}, {"0.0.0.0/1", wide}, {"128.0.0.0/1", wide}, {"10.0.0.0/7", wide},
		{"198.51.100.0/24", holds}, {"198.51.100.10/32", holds}, {"10.0.0.0/8", ""},
		{"198.51.101.0/24", ""}, {"198.51.100.0/29", ""},
	} {
		for _, env := range []map[string]string{up, goingDown} {
			route, files, tunnel := &fakeRoute{}, &fakeFiles{}, &fakeTunnel{}
			out, err := runOn(route, files, tunnel, running(), with(env, test.client))
			record := files.files[routing.Learned]
			switch {
			case err != nil:
				t.Errorf("%s, %s: %v", test.client, env["PLUTO_VERB"], err)
			case test.why == "" && env["PLUTO_VERB"] == "up-client":
				if !slices.Equal(route.calls, []string{"route " + test.client, "floor " +
					test.client}) || !strings.HasSuffix(record, "\nserver "+peer+"\n"+
					test.client+"\n") {
					t.Errorf("%s: did %q, and recorded %q", test.client, route.calls, record)
				}
			case test.why == "":
			case out != "Refused "+test.client+", which the server granted: "+test.why+"\n" ||
				len(route.calls) > 0 || len(tunnel.asked) > 0 || strings.Contains(record,
				"\n"+test.client+"\n"):
				t.Errorf("%s, %s: printed %q, did %q, asked %q, and recorded %q", test.client,
					env["PLUTO_VERB"], out, route.calls, tunnel.asked, record)
			case env["PLUTO_VERB"] == "up-client" && !strings.HasSuffix(record,
				"\nserver "+peer+"\n"):
				t.Errorf("%s: recorded %q, without the server's address", test.client, record)
			}
		}
	}
}

// Any other verb changes nothing, and says so.
func TestOtherVerbs(t *testing.T) {
	for _, verb := range []string{"up-host", "down-host", "up-client-v6", "down-client-v6",
		"up-host-v6", "UP-CLIENT"} {
		route := &fakeRoute{}
		out, err := run(route, map[string]string{"PLUTO_VERB": verb,
			"PLUTO_MY_SOURCEIP": "203.0.113.130"})
		want := "Nothing to do for PLUTO_VERB=" + verb + ": the route is for an IPv4 CHILD SA " +
			"with an address from the server\n"
		if err != nil || out != want || len(route.calls) > 0 {
			t.Errorf("%s: %q, %v, and did %q; want %q", verb, out, err, route.calls, want)
		}
	}
}

// Run fails without a verb, and without an IPv4 range in PLUTO_PEER_CLIENT for the verbs it acts
// on. Where the kernel refuses the route, it fails, and does nothing else.
func TestRunErrors(t *testing.T) {
	failed := errors.New("operation not permitted")
	noRange := func(client string) string {
		return "PLUTO_PEER_CLIENT is " + strconv.Quote(client) + ", no IPv4 range: strongSwan " +
			"names there the range the server granted"
	}
	for _, test := range []struct {
		name  string
		env   map[string]string
		err   error
		calls []string
		want  string
	}{
		{"no verb", map[string]string{"PLUTO_PEER_CLIENT": remote}, nil, nil,
			"PLUTO_VERB is not set: strongSwan runs updown as the tunnel goes up or down, and " +
				"says which in it"},
		{"no peer", map[string]string{"PLUTO_VERB": "up-client", "PLUTO_PEER_CLIENT": remote},
			nil, nil, `PLUTO_PEER is "", no address: strongSwan names there the VPN server's`},
		{"no range", with(up, ""), nil, nil, noRange("")},
		{"IPv6", with(up, "fd00::/8"), nil, nil, noRange("fd00::/8")},
		{"an address", with(goingDown, "192.0.2.10"), nil, nil, noRange("192.0.2.10")},
		{"route", up, failed, []string{set}, "cannot route " + remote + " through ikeward0 in " +
			"table 220: operation not permitted"},
		{"unroute", goingDown, failed, []string{unset}, "cannot remove the route to " +
			remote + " through ikeward0 from table 220: operation not permitted"},
	} {
		route := &fakeRoute{err: test.err}
		files := &fakeFiles{}
		out, err := runOn(route, files, &fakeTunnel{}, running(), test.env)
		if err == nil || err.Error() != test.want {
			t.Errorf("%s: %v, want %s", test.name, err, test.want)
		}
		if out != "" || !slices.Equal(route.calls, test.calls) || len(files.files) > 0 {
			t.Errorf("%s: printed %q, did %q, and wrote %q; want %q", test.name, out, route.calls,
				files.files, test.calls)
		}
	}
}

// Where the floor or the record fails, Run still routes the range, makes or records what it can,
// says what it did, and fails.
func TestLearnErrors(t *testing.T) {
	failed := errors.New("operation not permitted")
	readOnly := errors.New("read-only file system")
	for _, test := range []struct {
		name      string
		route     *fakeRoute
		files     *fakeFiles
		out, want string
		recorded  bool
	}{
		{"floor", &fakeRoute{floorErr: failed}, &fakeFiles{},
			"Routed " + remote + " through ikeward0 in table 220\n" + learned,
			"cannot make " + remote + " unreachable in table 220: operation not permitted", true},
		{"record", &fakeRoute{}, &fakeFiles{writeErr: readOnly}, routed,
			"cannot record " + remote + ": cannot write /var/lib/ikeward/learned-ranges: " +
				"read-only file system", false},
		{"both", &fakeRoute{floorErr: failed}, &fakeFiles{readErr: failed},
			"Routed " + remote + " through ikeward0 in table 220\n",
			"cannot make " + remote + " unreachable in table 220: operation not permitted\n" +
				"cannot record " + remote + ": cannot read /var/lib/ikeward/learned-ranges: " +
				"operation not permitted", false},
	} {
		out, err := runOn(test.route, test.files, &fakeTunnel{}, running(), up)
		if err == nil || err.Error() != test.want || out != test.out {
			t.Errorf("%s: %q, %v; want %q, %s", test.name, out, err, test.out, test.want)
		}
		_, recorded := test.files.files[routing.Learned]
		if !slices.Equal(test.route.calls, []string{set, floor}) || recorded != test.recorded {
			t.Errorf("%s: did %q, and recorded the range: %v", test.name, test.route.calls,
				recorded)
		}
	}
}

// Where Run cannot say what it did, it fails, though the change is made.
func TestFailingOutput(t *testing.T) {
	for _, state := range []string{"active", "deactivating"} {
		for _, verb := range []string{"up-client", "down-client", "up-host"} {
			route := &fakeRoute{}
			err := System{Route: route, Tunnel: &fakeTunnel{},
				Files: &fakeFiles{}, Runner: &fakeSystemd{state: state}, Out: failingWriter{},
				Env: func(key string) string {
					return map[string]string{"PLUTO_VERB": verb, "PLUTO_PEER": peer,
						"PLUTO_PEER_CLIENT": remote}[key]
				}}.Run(context.Background())
			if err == nil || err.Error() != "the pipe is closed" {
				t.Errorf("%s, %s: %v, want the pipe is closed", state, verb, err)
			}
		}
	}
}

// Each line updown prints, and each of an error's after main's "ikeward: ", fits with its newline
// in the 127 bytes strongSwan reads at a time. The journal would show a longer line in two
// entries. The causes are those host gives: netlink's, VICI's longest and its timeout, the
// record's, and systemctl's. The range is the longest an IPv4 one can be.
func TestLineLengths(t *testing.T) {
	const widest, server = "255.255.255.255/32", "255.255.255.254"
	upEnv := map[string]string{"PLUTO_VERB": "up-client", "PLUTO_PEER": server,
		"PLUTO_PEER_CLIENT": widest}
	downEnv := map[string]string{"PLUTO_VERB": "down-client", "PLUTO_PEER": server,
		"PLUTO_PEER_CLIENT": widest}
	noLink := errors.New("ikeward0: there is no such link")
	failed := errors.New("operation not permitted")
	timeout := &fakeTunnel{err: errors.New("list-sas: context deadline exceeded")}
	noSocket := &fakeTunnel{err: errors.New("/run/charon.vici: no such file or directory")}
	noBus := &fakeSystemd{err: errors.New("exit status 1: Failed to connect to bus")}
	upSAs := &fakeTunnel{sas: []status.IKESA{upSA("203.0.113.255", widest)}}
	full := &fakeFiles{writeErr: errors.New("no space left on device")}
	var lines []string
	for _, test := range []struct {
		route   *fakeRoute
		files   *fakeFiles
		tunnel  *fakeTunnel
		systemd *fakeSystemd
		env     map[string]string
	}{
		{&fakeRoute{}, &fakeFiles{}, &fakeTunnel{}, running(), upEnv},
		{&fakeRoute{}, &fakeFiles{}, &fakeTunnel{}, running(), with(upEnv, "128.0.0.0/1")},
		{&fakeRoute{}, &fakeFiles{}, &fakeTunnel{}, running(), with(upEnv, "255.255.255.0/24")},
		{&fakeRoute{}, full, &fakeTunnel{}, running(), with(upEnv, "255.255.255.0/24")},
		{&fakeRoute{}, &fakeFiles{}, upSAs, running(), downEnv},
		{&fakeRoute{}, &fakeFiles{}, &fakeTunnel{}, running(), downEnv},
		{&fakeRoute{}, &fakeFiles{}, &fakeTunnel{}, &fakeSystemd{state: "deactivating"}, downEnv},
		{&fakeRoute{}, &fakeFiles{}, &fakeTunnel{}, running(),
			map[string]string{"PLUTO_VERB": "down-client-v6"}},
		{&fakeRoute{}, &fakeFiles{}, &fakeTunnel{}, running(), with(upEnv, "fd00::/128")},
		{&fakeRoute{err: noLink}, &fakeFiles{}, &fakeTunnel{}, running(), upEnv},
		{&fakeRoute{floorErr: failed}, full, &fakeTunnel{}, running(), upEnv},
		{&fakeRoute{err: failed}, &fakeFiles{}, upSAs, running(), downEnv},
		{&fakeRoute{err: failed}, &fakeFiles{}, &fakeTunnel{}, &fakeSystemd{state: "deactivating"},
			downEnv},
		{&fakeRoute{}, &fakeFiles{}, timeout, running(), downEnv},
		{&fakeRoute{}, &fakeFiles{}, noSocket, running(), downEnv},
		{&fakeRoute{}, &fakeFiles{}, timeout, noBus, downEnv},
	} {
		out, err := runOn(test.route, test.files, test.tunnel, test.systemd, test.env)
		if err != nil {
			out += "ikeward: " + err.Error() + "\n"
		}
		lines = slices.AppendSeq(lines, strings.Lines(out))
	}
	// Each run prints a line, but the first, which learns the range too, and the refusal whose
	// record fails, print two. The one whose floor and record fail prints three. Those whose ask
	// fails print two, or three where systemctl fails too.
	if len(lines) != 24 {
		t.Errorf("printed %d lines, want 24:\n%s", len(lines), strings.Join(lines, ""))
	}
	for _, line := range lines {
		if len(line) > 127 {
			t.Errorf("%d bytes, past 127: %q", len(line), line)
		}
	}
}
