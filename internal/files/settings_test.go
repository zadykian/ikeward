package files

import (
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// SettingsFile reads back as the settings Generate wrote it from, with two domains or none of the
// floors as with one. Link's network sends each domain's names to the resolver.
func TestSettingsFile(t *testing.T) {
	two := Settings{
		DNS: netip.MustParseAddr("10.1.2.3"), Domains: []string{"example.test", "example.org"},
		Probe: "host.example.test",
		Floors: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"),
			netip.MustParsePrefix("172.16.0.0/12")},
	}
	none := testSettings
	none.Floors = nil
	for _, s := range []Settings{testSettings, two, none} {
		files, err := Generate(profiles["issued"], s)
		if err != nil {
			t.Fatal(err)
		}
		last := files[len(files)-1]
		if last.Path != SettingsFile {
			t.Fatalf("Generate's last file is %s, not %s", last.Path, SettingsFile)
		}
		read, err := ParseSettings(last.Data)
		if err != nil || !reflect.DeepEqual(read, s) {
			t.Errorf("%s reads as %+v, %v; want %+v:\n%s", SettingsFile, read, err, s, last.Data)
		}
		network := files[slices.IndexFunc(files, func(f File) bool {
			return f.Path == "/etc/systemd/network/25-"+Link+".network"
		})]
		for key, want := range map[string]string{"DNS": s.DNS.String(),
			"Domains": strings.Join(s.RoutingDomains(), " ")} {
			if got := unitValues(t, network, "Network", key); !slices.Equal(got, []string{want}) {
				t.Errorf("%s's %s is %q, want %s", network.Path, key, got, want)
			}
		}
	}
}

// Check refuses what install's flags must not give, and says why, each fault once.
func TestSettingsCheck(t *testing.T) {
	for _, test := range []struct {
		change func(*Settings)
		want   string
	}{
		{func(s *Settings) { s.DNS = netip.Addr{} }, "the resolver, invalid IP, is no IPv4 unicast"},
		{func(s *Settings) { s.DNS = netip.MustParseAddr("2001:db8::1") }, "2001:db8::1, is no IPv4"},
		{func(s *Settings) { s.DNS = netip.MustParseAddr("127.0.0.1") }, "127.0.0.1, is no IPv4"},
		{func(s *Settings) { s.DNS = netip.MustParseAddr("0.0.0.0") }, "0.0.0.0, is no IPv4"},
		{func(s *Settings) { s.Domains = nil }, "no domain"},
		{func(s *Settings) { s.Domains = []string{"a b"} }, `the domain "a b" is no domain name`},
		{func(s *Settings) { s.Domains = []string{"~x.example"} }, `"~x.example" is no domain`},
		{func(s *Settings) { s.Domains = []string{"X.example"} }, `"X.example" is no domain`},
		{func(s *Settings) { s.Domains = []string{"x.example."} }, `"x.example." is no domain`},
		{func(s *Settings) { s.Domains = []string{"-x.example"} }, `"-x.example" is no domain`},
		{func(s *Settings) { s.Domains = []string{strings.Repeat("a", 64)} }, "is no domain name"},
		{func(s *Settings) { s.Domains = []string{strings.Repeat("a.", 126) + "ab"} }, "no domain"},
		{func(s *Settings) { s.Domains = []string{"x.example", "x.example"} }, "x.example comes twice"},
		{func(s *Settings) { s.Probe = "" }, `the probe, "", is no domain name`},
		{func(s *Settings) { s.Domains, s.Probe = []string{"~x.example"}, "~x.example" },
			`the domain "~x.example" is no domain name`},
		{func(s *Settings) { s.Floors = []netip.Prefix{{}} }, "the floor invalid Prefix is no IPv4"},
		{func(s *Settings) { s.Floors = []netip.Prefix{netip.MustParsePrefix("fd00::/8")} },
			"the floor fd00::/8 is no IPv4 range"},
		{func(s *Settings) { s.Floors = []netip.Prefix{netip.MustParsePrefix("10.1.2.3/8")} },
			"the floor 10.1.2.3/8 is no range's first address: 10.0.0.0/8 is"},
		{func(s *Settings) { s.Floors = append(s.Floors, s.Floors[0]) },
			"the floor 10.88.0.0/16 comes twice"},
	} {
		s := testSettings
		s.Floors = slices.Clone(s.Floors)
		test.change(&s)
		err := s.Check()
		if err == nil || !strings.Contains(err.Error(), test.want) ||
			strings.Contains(err.Error(), "\n") {
			t.Errorf("%+v: Check() = %v; want %q alone", s, err, test.want)
			continue
		}
		if files, err := Generate(profiles["issued"], s); err == nil || files != nil ||
			!strings.HasPrefix(err.Error(), "cannot generate the files: ") {
			t.Errorf("%+v: Generate = %d files, %v; want Check's error", s, len(files), err)
		}
	}
	if err := testSettings.Check(); err != nil {
		t.Errorf("Check() = %v for testdata's settings", err)
	}
}

// ParseSettings refuses a file Generate does not write, naming the line where it can.
func TestParseSettingsErrors(t *testing.T) {
	const good = "dns 192.0.2.10\ndomain example.test\nprobe example.test\n"
	for _, test := range []struct{ data, want string }{
		{good + "port 53\n", `line 4: "port 53" is no setting`},
		{good + "dns 10.0.0.1\n", "line 4: a second resolver"},
		{good + "probe x.example\n", "line 4: a second probe"},
		{"dns 192.0.2\n" + good[strings.Index(good, "\n")+1:],
			`line 1: "dns 192.0.2": ParseAddr`},
		{good + "floor 10.0.0.0\n", `line 4: "floor 10.0.0.0": netip.ParsePrefix`},
		{good + "floor 10.0.0.0/8 \n", `line 4: "floor 10.0.0.0/8 "`},
		{"dns 192.0.2.10\nprobe example.test\n", "no domain"},
		{"domain example.test\nprobe example.test\n", "the resolver, invalid IP"},
		{"dns 192.0.2.10\ndomain example.test\n", `the probe, ""`},
		{"", "the resolver, invalid IP, is no IPv4 unicast address\nno domain\n"},
	} {
		s, err := ParseSettings([]byte(test.data))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%q: ParseSettings = %+v, %v; want %q", test.data, s, err, test.want)
		}
	}
}
