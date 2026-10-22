package routing

import (
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
)

// prefixes are the ranges ss name.
func prefixes(ss ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

// server is the integration test's VPN server's address, and given the floor that settings gives.
var (
	server = netip.MustParseAddr("198.51.100.10")
	given  = netip.MustParsePrefix("10.88.0.0/16")
)

// ReadLearned reads a range from each line, and a server's address from each "server" line, but
// comments and blank lines, and nothing where there is no record. It refuses a line that is
// neither, as a host's address with a length.
func TestReadLearned(t *testing.T) {
	for _, test := range []struct {
		name, data string
		want       Record
		err        string
	}{
		{"none", "", Record{}, ""},
		{"two", learned, Record{Ranges: prefixes("192.0.2.0/24", "198.18.0.0/24")}, ""},
		{"servers", "server 198.51.100.10\n\nserver ::ffff:203.0.113.5\nserver fd00::1\n" +
			"0.0.0.0/0\n", Record{Ranges: prefixes("0.0.0.0/0"), Servers: []netip.Addr{server,
			netip.MustParseAddr("203.0.113.5"), netip.MustParseAddr("fd00::1")}}, ""},
		{"a word", "# head\nnone\n", Record{}, Learned + `: line 2: "none" is no IPv4 range`},
		{"IPv6", "fd00::/8\n", Record{}, Learned + `: line 1: "fd00::/8" is no IPv4 range`},
		{"a host", "192.0.2.7/24\n", Record{},
			Learned + `: line 1: "192.0.2.7/24" is no IPv4 range`},
		{"a server's range", "server 198.51.100.0/24\n", Record{},
			Learned + `: line 1: "server 198.51.100.0/24" is no address`},
	} {
		f := &fakeFiles{}
		if test.data != "" {
			f.files = map[string]string{Learned: test.data}
		}
		got, err := ReadLearned(f)
		if test.err == "" && err != nil || test.err != "" && (err == nil || err.Error() != test.err) {
			t.Errorf("%s: %v, want %q", test.name, err, test.err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: %+v, want %+v", test.name, got, test.want)
		}
	}
	broken := errors.New("permission denied")
	if _, err := ReadLearned(&fakeFiles{err: broken}); !errors.Is(err, broken) ||
		err.Error() != "cannot read "+Learned+": permission denied" {
		t.Errorf("an unreadable record: %v", err)
	}
}

// Learn adds a range and a server's address the record lacks, keeping each part in order, and
// leaves the record alone where it holds both. An invalid range adds the server's address alone.
func TestLearn(t *testing.T) {
	f := &fakeFiles{}
	other := netip.MustParseAddr("198.51.100.9")
	for _, test := range []struct {
		server          netip.Addr
		dst             string
		added, rewrites bool
	}{
		{server, "198.18.0.0/24", true, true}, {server, "192.0.2.0/24", true, true},
		{server, "198.18.0.0/24", false, false}, {other, "", false, true},
		{other, "198.18.0.0/24", false, false},
	} {
		before := f.files[Learned]
		var dst netip.Prefix
		if test.dst != "" {
			dst = netip.MustParsePrefix(test.dst)
		}
		added, err := Learn(f, test.server, dst)
		if err != nil || added != test.added || (before != f.files[Learned]) != test.rewrites {
			t.Errorf("%v, %s: added %v: %v, and the record went from %q to %q", test.server,
				test.dst, added, err, before, f.files[Learned])
		}
	}
	want := "# The ranges the VPN server granted, which ikeward updown learned, and the VPN " +
		"server's addresses. ikeward routing makes each range unreachable below the route " +
		"through the tunnel, but one wider than /8 or holding a server's address.\n" +
		"server 198.51.100.9\nserver 198.51.100.10\n192.0.2.0/24\n198.18.0.0/24\n"
	if got := f.files[Learned]; got != want {
		t.Errorf("recorded %q, want %q", got, want)
	}

	broken := errors.New("read-only file system")
	if added, err := Learn(failingFiles{broken}, server, given); added ||
		!errors.Is(err, broken) || err.Error() != "cannot write "+Learned+": read-only file system" {
		t.Errorf("a record that cannot be written: added %v: %v", added, err)
	}
	f = &fakeFiles{files: map[string]string{Learned: "nonsense\n"}}
	if added, err := Learn(f, server, given); added || err == nil || f.files[Learned] !=
		"nonsense\n" {
		t.Errorf("a record that cannot be read: added %v: %v, and it holds %q", added, err,
			f.files[Learned])
	}
}

// failingFiles hold no file, and fail each write with err.
type failingFiles struct{ err error }

func (failingFiles) Read(string) ([]byte, fs.FileMode, error) { return nil, 0, fs.ErrNotExist }

func (f failingFiles) Write(string, []byte, fs.FileMode) error { return f.err }

// Floors puts the given floors first, and takes or passes over each range once. It passes over one
// wider than /8, as of every address, and one that holds a server's address, given or learned,
// and says why.
func TestFloors(t *testing.T) {
	wide := "wider than /8, it would take the server's own traffic"
	holds := "it holds 198.51.100.10, the VPN server's address"
	for _, test := range []struct {
		given  []netip.Prefix
		record Record
		floors []netip.Prefix
		passed []Passed
	}{
		{nil, Record{}, nil, nil},
		{[]netip.Prefix{given}, Record{}, []netip.Prefix{given}, nil},
		{[]netip.Prefix{given}, Record{Ranges: []netip.Prefix{given}}, []netip.Prefix{given}, nil},
		{nil, Record{Ranges: prefixes("198.18.0.0/24")}, prefixes("198.18.0.0/24"), nil},
		{prefixes("10.88.0.0/16", "198.51.0.0/16"), Record{Ranges: prefixes("0.0.0.0/0",
			"10.0.0.0/8", "10.88.41.0/24", "198.18.0.0/24", "198.51.100.0/24", "128.0.0.0/1",
			"198.51.101.0/24", "198.51.0.0/16", "0.0.0.0/0"), Servers: []netip.Addr{server}},
			prefixes("10.88.0.0/16", "10.0.0.0/8", "10.88.41.0/24", "198.18.0.0/24",
				"198.51.101.0/24"),
			[]Passed{{netip.MustParsePrefix("198.51.0.0/16"), holds},
				{netip.MustParsePrefix("0.0.0.0/0"), wide},
				{netip.MustParsePrefix("198.51.100.0/24"), holds},
				{netip.MustParsePrefix("128.0.0.0/1"), wide}}},
	} {
		floors, passed := test.record.Floors(test.given)
		if !slices.Equal(floors, test.floors) || !slices.Equal(passed, test.passed) {
			t.Errorf("%v, %+v: %v, passing over %q; want %v, passing over %q", test.given,
				test.record, floors, passed, test.floors, test.passed)
		}
	}
}

// Before any range is learned, as on a fresh install, SetFloors makes the settings' floor alone.
// Where it cannot read the record, it makes that floor all the same, then fails. Where it cannot
// read the settings, it makes the learned floors, then fails with ErrSettings.
func TestSetFloorsSettings(t *testing.T) {
	unreadable := installed("")
	unreadable.files[Learned] = "nonsense\n"
	noSettings := &fakeFiles{files: map[string]string{Learned: learned}}
	for _, test := range []struct {
		name        string
		f           *fakeFiles
		floors      []string
		err         string
		hasSettings bool
	}{
		{"none learned", installed(""), []string{"10.88.0.0/16"}, "", true},
		{"unreadable", unreadable, []string{"10.88.0.0/16"},
			Learned + `: line 1: "nonsense" is no IPv4 range`, true},
		{"no settings", noSettings, []string{"192.0.2.0/24", "198.18.0.0/24"},
			ErrSettings.Error() + ", which install writes: file does not exist", false},
	} {
		k := &fakeKernel{}
		var out, want strings.Builder
		err := System{Kernel: k, Files: test.f, Out: &out}.SetFloors()
		if test.err == "" && err != nil || test.err != "" && (err == nil || err.Error() != test.err) ||
			errors.Is(err, ErrSettings) == test.hasSettings {
			t.Errorf("%s: %v, want %q", test.name, err, test.err)
		}
		var changes []string
		for _, dst := range test.floors {
			changes = append(changes, "floor "+dst)
			fmt.Fprintf(&want, "Made %s unreachable in table 220: unreachable %s metric 4000\n",
				dst, dst)
		}
		if !slices.Equal(k.changes, changes) || out.String() != want.String() {
			t.Errorf("%s: changed %q, and printed %q", test.name, k.changes, out.String())
		}
	}
}

// A range that Refusal refuses takes no floor, as one written by hand in the record or the
// settings. SetFloors names it once, the files that hold it and why, and makes the others.
func TestSetFloorsPassesOver(t *testing.T) {
	k := &fakeKernel{}
	var out strings.Builder
	f := installed("server 198.51.100.10\n0.0.0.0/1\n198.18.0.0/24\n198.51.100.0/24\n")
	f.files[files.SettingsFile] += "floor 198.51.0.0/16\nfloor 0.0.0.0/1\n"
	if err := (System{Kernel: k, Files: f, Out: &out}).SetFloors(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"floor 10.88.0.0/16", "floor 198.18.0.0/24"}; !slices.Equal(k.changes,
		want) {
		t.Errorf("changed %q, want %q", k.changes, want)
	}
	want := "Passed over 198.51.0.0/16 in " + files.SettingsFile + ": it holds 198.51.100.10, " +
		"the VPN server's address\nPassed over 0.0.0.0/1 in " + files.SettingsFile + " and " +
		Learned + ": wider than /8, it would take the server's own traffic\n" +
		"Passed over 198.51.100.0/24 in " + Learned + ": it holds 198.51.100.10, " +
		"the VPN server's address\n" +
		"Made 10.88.0.0/16 unreachable in table 220: unreachable 10.88.0.0/16 metric 4000\n" +
		"Made 198.18.0.0/24 unreachable in table 220: unreachable 198.18.0.0/24 metric 4000\n"
	if got := out.String(); got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}
