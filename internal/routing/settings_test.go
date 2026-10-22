package routing

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
)

// ReadSettings reads files.SettingsFile as install writes it. A file that is missing, that cannot
// be read or that files.ParseSettings refuses fails it with ErrSettings, and the cause.
func TestReadSettings(t *testing.T) {
	got, err := ReadSettings(installed(""))
	want := files.Settings{DNS: netip.MustParseAddr("192.0.2.10"),
		Domains: []string{"example.test"}, Probe: "example.test", Floors: []netip.Prefix{given}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ReadSettings = %+v, %v; want %+v", got, err, want)
	}
	broken := errors.New("input/output error")
	for _, test := range []struct {
		f    *fakeFiles
		want string
	}{
		{&fakeFiles{}, ", which install writes: file does not exist"},
		{&fakeFiles{err: broken}, ", which install writes: input/output error"},
		{&fakeFiles{files: map[string]string{files.SettingsFile: "dns 192.0.2.10\n"}},
			": no domain\nthe probe, \"\", is no domain name"},
	} {
		got, err := ReadSettings(test.f)
		if !errors.Is(err, ErrSettings) || err.Error() != "cannot read "+files.SettingsFile+
			test.want || !reflect.DeepEqual(got, files.Settings{}) {
			t.Errorf("ReadSettings = %+v, %v; want %q", got, err, test.want)
		}
	}
}
