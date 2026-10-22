package status

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/routing"
)

// The NAT check gives the rule's connections while the table is in place. With the table gone,
// as after nft flush ruleset, or not ikeward's, it fails and says what a forwarded network loses.
// Where nftables won't list the table to a user other than root, the table may be in place: the
// line gives the error alone. Either way it fails alone, as the tunnel still carries traffic.
func TestNATCheck(t *testing.T) {
	for _, test := range []struct {
		name string
		nat  fakeNAT
		want string
	}{
		{"no table", fakeNAT{err: routing.Missing{Err: errors.New("no table ip ikeward")}},
			"FAIL  NAT       no table ip ikeward: a forwarded network's traffic leaves ikeward0 " +
				"from its own address, which no SA takes"},
		{"unreadable", fakeNAT{
			err: errors.New("cannot read table ip ikeward: operation not permitted")},
			"FAIL  NAT       cannot read table ip ikeward: operation not permitted"},
		{"connections", fakeNAT{connections: 12},
			"ok    NAT       " + masquerading + "; connections so far: 12"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := healthy()
			*f.nat = test.nat
			results := f.system().Check(t.Context())
			want := slices.Clone(capturedLines)
			want[slices.IndexFunc(results, func(r Result) bool { return r.Name == NATCheck })] =
				test.want
			if got := lines(results); !slices.Equal(got, want) {
				t.Errorf("printed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			failed := 0
			if strings.HasPrefix(test.want, "FAIL") {
				failed = 1
			}
			if got := Failed(results); got != failed {
				t.Errorf("Failed is %d, want %d", got, failed)
			}
		})
	}
}
