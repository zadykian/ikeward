package routing

import (
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
)

// fakeKernel is the kernel's rules, which it lists as rules, and the floor. It records each change,
// keeping the rules up to date, and fails each call to the method that fail names with err.
type fakeKernel struct {
	rules   []Rule
	changes []string
	fail    string
	err     error
}

func (k *fakeKernel) Rules() ([]Rule, error) {
	if k.fail == "Rules" {
		return nil, k.err
	}
	return slices.Clone(k.rules), nil
}

func (k *fakeKernel) AddRule(rule Rule) error {
	k.changes = append(k.changes, "add "+rule.String())
	if k.fail == "AddRule" {
		return k.err
	}
	k.rules = append(k.rules, rule)
	return nil
}

func (k *fakeKernel) RemoveRule(rule Rule) error {
	k.changes = append(k.changes, "remove "+rule.String())
	if k.fail == "RemoveRule" {
		return k.err
	}
	k.rules = slices.DeleteFunc(k.rules, func(r Rule) bool { return r == rule })
	return nil
}

func (k *fakeKernel) SetFloor(dst netip.Prefix) error {
	k.changes = append(k.changes, "floor "+dst.String())
	if k.fail == "SetFloor" {
		return k.err
	}
	return nil
}

// fakeNAT is NATTable, which counts the times Set made it, and fails each with err.
type fakeNAT struct {
	sets int
	err  error
}

func (f *fakeNAT) Set() error {
	f.sets++
	return f.err
}

// fakeFiles are the files under the root directory, by path, which Write replaces. Read fails
// with err, where set.
type fakeFiles struct {
	files map[string]string
	err   error
}

func (f *fakeFiles) Read(path string) ([]byte, fs.FileMode, error) {
	data, ok := f.files[path]
	switch {
	case f.err != nil:
		return nil, 0, f.err
	case !ok:
		return nil, 0, fs.ErrNotExist
	}
	return []byte(data), 0o644, nil
}

func (f *fakeFiles) Write(path string, data []byte, _ fs.FileMode) error {
	if f.files == nil {
		f.files = map[string]string{}
	}
	f.files[path] = string(data)
	return nil
}

// learned is Learned as updown writes it, with two ranges.
const learned = "# The ranges the VPN server granted.\n192.0.2.0/24\n198.18.0.0/24\n"

// settings is files.SettingsFile as install writes it, with one floor.
const settings = "# Written by ikeward install.\ndns 192.0.2.10\ndomain example.test\n" +
	"probe example.test\nfloor 10.88.0.0/16\n"

// installed are the files of a server where install ran, with learned as the record, unless empty.
func installed(learned string) *fakeFiles {
	f := &fakeFiles{files: map[string]string{files.SettingsFile: settings}}
	if learned != "" {
		f.files[Learned] = learned
	}
	return f
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("the pipe is closed") }

// The kernel's own rules, as at boot.
var kernelRules = []Rule{{0, 255, -1}, {32766, Main, -1}, {32767, 253, -1}}

// strongSwan's rule, as charon made it before ikeward set routing_table to 0.
var charons = Rule{Priority: 220, Table: 220, SuppressPrefixlen: -1}

// Run makes the NAT table, the floors, then the rules, and says so, as at boot. The rules come in
// order, the server's own networks first. The settings' floor comes first, then one for each
// learned range.
func TestRun(t *testing.T) {
	k := &fakeKernel{rules: slices.Clone(kernelRules)}
	n := &fakeNAT{}
	var out strings.Builder
	if err := (System{Kernel: k, NAT: n, Files: installed(learned), Out: &out}).Run(); err != nil {
		t.Fatal(err)
	}
	if n.sets != 1 {
		t.Errorf("made the table %d times, want once", n.sets)
	}
	wantChanges := []string{"floor 10.88.0.0/16", "floor 192.0.2.0/24", "floor 198.18.0.0/24",
		"add 32000: from all lookup main suppress_prefixlength 0",
		"add 32001: from all lookup 220 suppress_prefixlength 0"}
	if !slices.Equal(k.changes, wantChanges) {
		t.Errorf("changed %q, want %q", k.changes, wantChanges)
	}
	want := "Made table ip ikeward: oifname \"ikeward0\" counter masquerade\n" +
		"Made 10.88.0.0/16 unreachable in table 220: unreachable 10.88.0.0/16 metric 4000\n" +
		"Made 192.0.2.0/24 unreachable in table 220: unreachable 192.0.2.0/24 metric 4000\n" +
		"Made 198.18.0.0/24 unreachable in table 220: unreachable 198.18.0.0/24 metric 4000\n" +
		"Added rule 32000: from all lookup main suppress_prefixlength 0\n" +
		"Added rule 32001: from all lookup 220 suppress_prefixlength 0\n"
	if got := out.String(); got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}

// SetRules keeps the rules in place, and adds only those missing. It removes strongSwan's rule,
// which put the remote network first, once ikeward's are in, and any other rule of table 220.
func TestSetRules(t *testing.T) {
	other := Rule{Priority: 32500, Table: 220, SuppressPrefixlen: 0}
	for _, test := range []struct {
		name    string
		rules   []Rule
		changes []string
		out     string
	}{
		{"in place", append(slices.Clone(kernelRules), Rules...), nil, ""},
		{"one missing", append(slices.Clone(kernelRules), Rules[1]),
			[]string{"add 32000: from all lookup main suppress_prefixlength 0"},
			"Added rule 32000: from all lookup main suppress_prefixlength 0\n"},
		{"strongSwan's", append(slices.Clone(kernelRules), charons, other, Rules[0]),
			[]string{"add 32001: from all lookup 220 suppress_prefixlength 0",
				"remove 220: from all lookup 220", "remove " + other.String()},
			"Added rule 32001: from all lookup 220 suppress_prefixlength 0\n" +
				"Removed rule 220: from all lookup 220, which looked up table 220 too\n" +
				"Removed rule 32500: from all lookup 220 suppress_prefixlength 0, which looked up " +
				"table 220 too\n"},
	} {
		k := &fakeKernel{rules: test.rules}
		var out strings.Builder
		if err := (System{Kernel: k, Out: &out}).SetRules(); err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if !slices.Equal(k.changes, test.changes) || out.String() != test.out {
			t.Errorf("%s: changed %q, and printed %q; want %q, and %q", test.name, k.changes,
				out.String(), test.changes, test.out)
		}
		if got := append(slices.Clone(kernelRules), Rules...); !sameRules(k.rules, got) {
			t.Errorf("%s: left %v, want %v", test.name, k.rules, got)
		}
	}
}

// sameRules is whether a and b hold the same rules, in any order.
func sameRules(a, b []Rule) bool {
	compare := func(x, y Rule) int { return x.Priority - y.Priority }
	return slices.Equal(slices.SortedFunc(slices.Values(a), compare),
		slices.SortedFunc(slices.Values(b), compare))
}

// Where the kernel refuses a change, Run names it, and goes on with the parts after it: the rules
// alone put the server's own networks first. Where it cannot print, it fails, the change made.
func TestRunErrors(t *testing.T) {
	refused := errors.New("operation not permitted")
	rules := []string{"add 32000: from all lookup main suppress_prefixlength 0",
		"add 32001: from all lookup 220 suppress_prefixlength 0",
		"remove 220: from all lookup 220"}
	for _, test := range []struct {
		fail, want string
		changes    []string
	}{
		{"NAT", "cannot make table ip ikeward: operation not permitted",
			append([]string{"floor 10.88.0.0/16"}, rules...)},
		{"SetFloor", "cannot make 10.88.0.0/16 unreachable in table 220: operation not permitted",
			append([]string{"floor 10.88.0.0/16"}, rules...)},
		{"Rules", "cannot read the rules: operation not permitted", []string{"floor 10.88.0.0/16"}},
		{"AddRule", "cannot add rule 32000: from all lookup main suppress_prefixlength 0: " +
			"operation not permitted", []string{"floor 10.88.0.0/16",
			"add 32000: from all lookup main suppress_prefixlength 0"}},
		{"RemoveRule", "cannot remove rule 220: from all lookup 220: operation not permitted",
			[]string{"floor 10.88.0.0/16",
				"add 32000: from all lookup main suppress_prefixlength 0",
				"add 32001: from all lookup 220 suppress_prefixlength 0",
				"remove 220: from all lookup 220"}},
	} {
		k := &fakeKernel{rules: append(slices.Clone(kernelRules), charons), fail: test.fail,
			err: refused}
		n := &fakeNAT{}
		if test.fail == "NAT" {
			n.err = refused
		}
		err := System{Kernel: k, NAT: n, Files: installed(""), Out: &strings.Builder{}}.Run()
		if err == nil || err.Error() != test.want || !errors.Is(err, refused) {
			t.Errorf("%s: %v, want %s", test.fail, err, test.want)
		}
		if !slices.Equal(k.changes, test.changes) {
			t.Errorf("%s: changed %q, want %q", test.fail, k.changes, test.changes)
		}
	}

	for name, set := range map[string]func(System) error{"SetNAT": System.SetNAT,
		"SetFloors": System.SetFloors, "SetRules": System.SetRules} {
		k := &fakeKernel{rules: slices.Clone(kernelRules)}
		n := &fakeNAT{}
		err := set(System{Kernel: k, NAT: n, Files: installed(""), Out: failingWriter{}})
		if err == nil || err.Error() != "the pipe is closed" || n.sets+len(k.changes) != 1 {
			t.Errorf("%s: %v, having made %d changes; want the pipe is closed, and one", name,
				err, n.sets+len(k.changes))
		}
	}
}

// A rule lists as ip rule lists it, the main table by its name.
func TestRuleString(t *testing.T) {
	for rule, want := range map[Rule]string{
		Rules[0]: "32000: from all lookup main suppress_prefixlength 0",
		Rules[1]: "32001: from all lookup 220 suppress_prefixlength 0",
		charons:  "220: from all lookup 220",
	} {
		if got := rule.String(); got != want {
			t.Errorf("%#v: %q, want %q", rule, got, want)
		}
	}
}

// Missing says what its error says, which errors.Is finds through it.
func TestMissing(t *testing.T) {
	reason := errors.New("no table ip ikeward")
	var err error = Missing{reason}
	if err.Error() != "no table ip ikeward" || !errors.Is(err, reason) {
		t.Errorf("%v, want no table ip ikeward, wrapping it", err)
	}
	if !errors.As(fmt.Errorf("status: %w", err), &Missing{}) {
		t.Errorf("errors.As finds no Missing in a wrapped one")
	}
}
