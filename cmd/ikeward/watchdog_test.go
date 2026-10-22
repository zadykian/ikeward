package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/host"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/status"
)

// restart is the command that restarts strongSwan.
const restart = "systemctl restart --no-block strongswan.service"

// fakeVPN is strongSwan, up or down, which systemctl restarts, and up after a restart where it
// mends; as a runner, it prints resolved for resolvectl, and fails systemctl with failRestart.
type fakeVPN struct {
	up, mends   bool
	commands    []string
	failRestart error
}

func (v *fakeVPN) IKESAs(context.Context, string) ([]status.IKESA, error) {
	if !v.up {
		return nil, errors.New("/run/charon.vici: no such file or directory")
	}
	return up.sas, nil
}

func (v *fakeVPN) Run(name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	v.commands = append(v.commands, line)
	if line == restart {
		if v.failRestart != nil {
			return "", v.failRestart
		}
		v.up = v.mends
	}
	return resolved, nil
}

// fakeClock is a clock whose sleeps pass at once.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return nil
}

// watchdogSystem is a system as root whose VPN is vpn, at the time clock gives, and whose files
// are under root. ikeward's routing is in place.
func watchdogSystem(vpn *fakeVPN, clock *fakeClock, root string, out *strings.Builder) system {
	return system{stdout: out, tunnel: vpn, network: newKernel(), resolver: answering{},
		runner: vpn, files: host.Root(root), clock: clock, nat: &fakeNAT{},
		euid: func() int { return 0 }}
}

// failing are the lines of the checks that fail while strongSwan is stopped. The fake kernel keeps
// the route, but no CHILD SA names the ranges to look for.
const failing = `FAIL  IKE SA    cannot ask strongSwan: /run/charon.vici: no such file or directory
FAIL  CHILD SA  not known, as strongSwan could not be asked
FAIL  address   no address from the server to look for on ikeward0: strongSwan could not be asked
FAIL  route     no range of the server's to look for in table 220: strongSwan could not be asked
FAIL  resolver  not asked, as the tunnel's checks failed
`

// watchdog prints nothing while the VPN is up. Where the VPN is down, it restarts strongSwan,
// which it records under the root directory, and says whether that mended it. It does not
// restart strongSwan again within 10 minutes, and then exits 1.
func TestWatchdogCommand(t *testing.T) {
	root := installedRoot(t)
	clock := &fakeClock{now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	vpn := &fakeVPN{up: true}
	var out strings.Builder
	if err := run([]string{"watchdog"}, watchdogSystem(vpn, clock, root, &out)); err != nil {
		t.Fatal(err)
	}
	if out.Len() > 0 || slices.Contains(vpn.commands, restart) {
		t.Errorf("printed %q, and ran %q", out.String(), vpn.commands)
	}

	vpn.up, vpn.mends = false, true
	if err := run([]string{"watchdog"}, watchdogSystem(vpn, clock, root, &out)); err != nil {
		t.Fatal(err)
	}
	want := failing + "Running " + restart + "\n" +
		"Every check passes 2s after the restart\n"
	if got := out.String(); got != want {
		t.Errorf("printed:\n%s\nwant:\n%s", got, want)
	}
	data, err := os.ReadFile(filepath.Join(root, install.LastRestart))
	if err != nil || string(data) != "2026-10-05T12:00:00Z\n" {
		t.Errorf("%s holds %q, %v", install.LastRestart, data, err)
	}

	out.Reset()
	vpn.up, vpn.commands = false, nil
	clock.now = clock.now.Add(5 * time.Minute)
	err = run([]string{"watchdog"}, watchdogSystem(vpn, clock, root, &out))
	if err == nil || err.Error() != "5 of 9 checks failed" || exitStatus(err) != 1 {
		t.Errorf("%v, want 5 of 9 checks failed, and exit status 1", err)
	}
	want = failing + "Leaving strongSwan as it is: the watchdog restarted it 5m2s ago, and does " +
		"so once in 10 minutes at most\n"
	if got := out.String(); got != want {
		t.Errorf("printed:\n%s\nwant:\n%s", got, want)
	}
	if slices.Contains(vpn.commands, restart) {
		t.Errorf("ran %q", vpn.commands)
	}
}

// refusing is the remote network's DNS server, answering REFUSED, as host.DNS says it.
type refusing struct{}

func (refusing) Lookup(context.Context, string, string) error {
	return fmt.Errorf("%w REFUSED", status.ErrAnswer)
}

// Where the remote resolver answers with an error through the tunnel, watchdog leaves
// strongSwan alone, which a restart would take down and not mend, says why, and exits 1.
func TestWatchdogResolverAnswers(t *testing.T) {
	vpn := &fakeVPN{up: true}
	var out strings.Builder
	sys := watchdogSystem(vpn, &fakeClock{}, installedRoot(t), &out)
	sys.resolver = refusing{}
	err := run([]string{"watchdog"}, sys)
	if err == nil || err.Error() != "1 of 9 checks failed" || exitStatus(err) != 1 {
		t.Errorf("%v, want 1 of 9 checks failed, and exit status 1", err)
	}
	want := "FAIL  resolver  192.0.2.10 gives no address for example.test: it answers " +
		"REFUSED through the tunnel\nLeaving strongSwan as it is: a restart does not change " +
		"the remote resolver's answer, which came through the tunnel\n"
	if got := out.String(); got != want {
		t.Errorf("printed:\n%s\nwant:\n%s", got, want)
	}
	if slices.Contains(vpn.commands, restart) {
		t.Errorf("ran %q", vpn.commands)
	}
}

// watchdog runs as root alone, and checks nothing otherwise.
func TestWatchdogNotRoot(t *testing.T) {
	vpn := &fakeVPN{}
	var out strings.Builder
	sys := watchdogSystem(vpn, &fakeClock{}, installedRoot(t), &out)
	sys.euid = func() int { return 1000 }
	err := run([]string{"watchdog"}, sys)
	if err == nil || err.Error() != "watchdog restarts strongSwan, and must run as root" ||
		exitStatus(err) != 3 {
		t.Errorf("%v, want watchdog restarts strongSwan, and must run as root, and exit status 3",
			err)
	}
	if out.Len() > 0 || len(vpn.commands) > 0 {
		t.Errorf("printed %q, and ran %q", out.String(), vpn.commands)
	}
}

// Where watchdog fails itself, it exits 3, which its service, unlike 1, counts as a failure.
func TestWatchdogFails(t *testing.T) {
	for _, test := range []struct {
		name  string
		setUp func(t *testing.T, root string, vpn *fakeVPN)
		want  string
	}{
		{"record unread", func(t *testing.T, root string, _ *fakeVPN) {
			t.Helper()
			if err := os.MkdirAll(filepath.Join(root, install.LastRestart), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "cannot tell when the watchdog last restarted strongSwan: cannot read " +
			install.LastRestart + ": not a regular file"},
		{"record unwritten", func(t *testing.T, root string, _ *fakeVPN) {
			t.Helper()
			dir := filepath.Dir(filepath.Join(root, install.LastRestart))
			if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
				t.Fatal(err)
			}
			// A link to nothing where its directory should be holds no record, and fails the
			// write even as root. The settings go too, and the floor's repair fails first.
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "gone"), dir); err != nil {
				t.Fatal(err)
			}
		}, "cannot record the restart in " + install.LastRestart},
		{"restart", func(_ *testing.T, _ string, vpn *fakeVPN) {
			vpn.failRestart = errors.New("exit status 5: Unit strongswan.service not found.")
		}, "systemctl restart --no-block strongswan.service: exit status 5: Unit " +
			"strongswan.service not found."},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := installedRoot(t)
			vpn := &fakeVPN{}
			test.setUp(t, root, vpn)
			var out strings.Builder
			err := run([]string{"watchdog"}, watchdogSystem(vpn, &fakeClock{}, root, &out))
			if err == nil || !strings.Contains(err.Error(), test.want) || exitStatus(err) != 3 {
				t.Errorf("%v, want %s, and exit status 3", err, test.want)
			}
		})
	}
}

// Where the NAT table is missing, watchdog makes it again, says so, and exits 0, leaving the
// tunnel alone. Where it cannot make the table, it fails itself, and exits 3.
func TestWatchdogRouting(t *testing.T) {
	vpn := &fakeVPN{up: true}
	nat := &fakeNAT{missing: true}
	var out strings.Builder
	sys := watchdogSystem(vpn, &fakeClock{}, installedRoot(t), &out)
	sys.nat = nat
	if err := run([]string{"watchdog"}, sys); err != nil {
		t.Fatal(err)
	}
	want := "FAIL  NAT       no table ip ikeward: a forwarded network's traffic leaves ikeward0 " +
		"from its own address, which no SA takes\n" +
		"Made table ip ikeward: oifname \"ikeward0\" counter masquerade\n" +
		"Every check passes, with what was missing made again\n"
	if got := out.String(); got != want {
		t.Errorf("printed:\n%s\nwant:\n%s", got, want)
	}
	if want := []string{"connections", "set", "connections"}; !slices.Equal(nat.calls, want) ||
		slices.Contains(vpn.commands, restart) {
		t.Errorf("did %q to the table, and ran %q; want %q, and no restart", nat.calls,
			vpn.commands, want)
	}

	sys.nat = &fakeNAT{missing: true, err: errors.New("operation not permitted")}
	err := run([]string{"watchdog"}, sys)
	want = "cannot make table ip ikeward: operation not permitted"
	if err == nil || err.Error() != want || exitStatus(err) != 3 {
		t.Errorf("%v, want %s, and exit status 3", err, want)
	}
}
