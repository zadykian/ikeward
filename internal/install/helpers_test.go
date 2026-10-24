package install

import (
	"bytes"
	"errors"
	"io"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/profile"
	"github.com/zadykian/ikeward/internal/profile/profiletest"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// newProfile is a profile issued for name, as Parse reads it.
func newProfile(t *testing.T, name string) *profile.Profile {
	t.Helper()
	p, err := profile.Parse(profiletest.Files(t, name, ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// settings are the remote network's values that install's flags give.
var settings = files.Settings{
	DNS: netip.MustParseAddr("192.0.2.10"), Domains: []string{"example.test"},
	Probe: "example.test", Floors: []netip.Prefix{netip.MustParsePrefix("10.88.0.0/16")},
}

// want is what install writes for p, in its order.
func want(t *testing.T, p *profile.Profile) []files.File {
	t.Helper()
	generated, err := files.Generate(p, settings)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := files.Credentials(p)
	if err != nil {
		t.Fatal(err)
	}
	return append(generated, credentials...)
}

// pathsOf are the paths of fs.
func pathsOf(fs []files.File) []string {
	var paths []string
	for _, f := range fs {
		paths = append(paths, f.Path)
	}
	return paths
}

// outputLines are out's lines but the profile's summary, which starts it.
func outputLines(t *testing.T, out string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) < 5 || lines[0] != "The profile gives:" {
		t.Fatalf("the output does not start with the profile's summary:\n%s", out)
	}
	i := 1
	for i < len(lines) && strings.HasPrefix(lines[i], "  ") {
		i++
	}
	return lines[i:]
}

// recordOf is Record as listing paths.
func recordOf(paths ...string) []byte {
	return []byte("# The files ikeward install wrote, which ikeward uninstall removes.\n" +
		strings.Join(paths, "\n") + "\n")
}

// journalOf is Journal as listing commands.
func journalOf(commands ...string) []byte {
	return []byte("# The steps ikeward install owes, which a rerun takes where it stops half way.\n" +
		strings.Join(commands, "\n") + "\n")
}

// pendingOf is Pending as listing units.
func pendingOf(units ...string) []byte {
	return []byte("# The daemons whose settings ikeward install changes, which a rerun " +
		"restarts where it stops half way.\n" + strings.Join(units, "\n") + "\n")
}

// installOn runs install for p on f and r, which must succeed, then clears what they recorded.
func installOn(t *testing.T, f *fakeFiles, r *fakeRunner, p *profile.Profile) {
	t.Helper()
	if err := onHome(f, r, &bytes.Buffer{}).Install(p, settings); err != nil {
		t.Fatal(err)
	}
	f.writes, f.removes, r.commands = nil, nil, nil
}

// onHome is install's system on f and r, on home's kernel, with ikeward's NAT table, saying what
// it does on out.
func onHome(f *fakeFiles, r *fakeRunner, out io.Writer) System {
	return System{Files: f, Runner: r, Out: out, Network: home(), Tables: withNAT()}
}

// dryRunOn is onHome's system in a dry run.
func dryRunOn(f *fakeFiles, r *fakeRunner, out io.Writer) System {
	s := onHome(f, r, out)
	s.DryRun = true
	return s
}

// withNAT are tables with ikeward's NAT table, as ikeward routing makes it.
func withNAT() *fakeTables { return &fakeTables{tables: map[string]bool{"ikeward": true}} }

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("the pipe is closed") }

// checkLines fails t where got's lines are not want.
func checkLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n  %s\nwant:\n  %s", what, strings.Join(got, "\n  "),
			strings.Join(want, "\n  "))
	}
}

// home is a kernel as ikeward leaves it: the kernel's rules with ikeward's, and table 220's route
// and floor. It has the tunnel's link too, as networkd makes it once install has run.
func home() *fakeNetwork {
	subnet := settings.Floors[0]
	return &fakeNetwork{
		rules: []routing.Rule{{Priority: 0, Table: 255, SuppressPrefixlen: -1}, routing.Rules[0],
			routing.Rules[1], {Priority: 32766, Table: routing.Main, SuppressPrefixlen: -1}},
		routes: map[int][]status.Route{files.Table: {
			{Dst: subnet, Link: files.Link, Type: status.Unicast},
			{Dst: subnet, Type: status.Unreachable, Metric: routing.FloorMetric}}},
		links: map[string]bool{files.Link: true},
	}
}
