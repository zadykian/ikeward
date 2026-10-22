package files

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// charon tries again until the tunnel connects: keyingtries 0 starts a new attempt where the
// server leaves one unanswered, and retry_initiate_interval where its name does not resolve. The
// child restarts where the server stops answering, and its close_action stays none, for the
// reason the connection's comment gives.
func TestGenerateRetries(t *testing.T) {
	child := "connections." + Connection + ".children." + Connection + "."
	want := map[string]map[string]string{
		templates[0].Name(): {
			"connections." + Connection + ".keyingtries": "0",
			child + "start_action":                       "start",
			child + "dpd_action":                         "restart",
			child + "close_action":                       "none",
		},
		templates[1].Name(): {"charon.retry_initiate_interval": "30s"},
	}
	for name, p := range profiles {
		files, err := Generate(p, testSettings)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files[:2] {
			read := settings(t, f)
			for key, value := range want[f.Path] {
				if got, ok := read[key]; !ok || got != value {
					t.Errorf("%s: %s: %s is %q, want %q", name, f.Path, key, got, value)
				}
			}
		}
	}
}

// Link's network gives it LinkULA alone: an IPv6 /128 in fd00::/8, the unique local prefixes of
// RFC 4193. An IPv4 address would become the source of the lookups on Link. The drop-in has charon
// ignore the link, but for the address from the server, which goes there.
func TestGenerateLinkULA(t *testing.T) {
	ula, err := netip.ParsePrefix(LinkULA)
	if err != nil || !ula.Addr().Is6() || ula.Addr().Is4In6() || ula.Bits() != 128 ||
		!netip.MustParsePrefix("fd00::/8").Contains(ula.Addr()) {
		t.Fatalf("LinkULA is %q, not an IPv6 /128 in fd00::/8: %v", LinkULA, err)
	}
	network := "/etc/systemd/network/25-" + Link + ".network"
	for name, p := range profiles {
		files, err := Generate(p, testSettings)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(files, func(f File) bool { return f.Path == network })
		if i < 0 {
			t.Fatalf("%s: Generate returns no %s", name, network)
		}
		if got := unitValues(t, files[i], "Network", "Address"); !slices.Equal(got,
			[]string{LinkULA}) {
			t.Errorf("%s: %s gives the addresses %q, want %s alone", name, network, got, LinkULA)
		}
		read := settings(t, files[1])
		for _, key := range []string{"charon.install_virtual_ip_on", "charon.interfaces_ignore"} {
			if read[key] != Link {
				t.Errorf("%s: %s: %s is %q, want %s", name, files[1].Path, key, read[key], Link)
			}
		}
	}
}

// Link is an XFRM interface on no underlying link, whose ID the connection's SAs take both ways.
func TestGenerateInterface(t *testing.T) {
	id := strconv.Itoa(InterfaceID)
	netdev := "/etc/systemd/network/25-" + Link + ".netdev"
	child := "connections." + Connection + ".children." + Connection + "."
	for name, p := range profiles {
		files, err := Generate(p, testSettings)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(files, func(f File) bool { return f.Path == netdev })
		if i < 0 {
			t.Fatalf("%s: Generate returns no %s", name, netdev)
		}
		for _, want := range []struct{ section, key, value string }{{"NetDev", "Name", Link},
			{"NetDev", "Kind", "xfrm"}, {"Xfrm", "InterfaceId", id}, {"Xfrm", "Independent", "yes"},
		} {
			if got := unitValues(t, files[i], want.section, want.key); !slices.Equal(got,
				[]string{want.value}) {
				t.Errorf("%s: %s: %s's %s is %q, want %s", name, netdev, want.section, want.key, got,
					want.value)
			}
		}
		read := settings(t, files[0])
		for _, key := range []string{child + "if_id_in", child + "if_id_out"} {
			if read[key] != id {
				t.Errorf("%s: %s is %q, want %s", name, key, read[key], id)
			}
		}
	}
}

// unitValues returns the values of key in section of f, a systemd file of [Section] headers and
// KEY=VALUE lines. They come in order, as networkd reads a key that may come again.
func unitValues(t *testing.T, f File, section, key string) []string {
	t.Helper()
	var values []string
	in := ""
	for line := range strings.Lines(string(f.Data)) {
		line = strings.TrimSpace(line)
		k, v, isSetting := strings.Cut(line, "=")
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			in = strings.Trim(line, "[]")
		case isSetting && in != "":
			if in == section && k == key {
				values = append(values, v)
			}
		default:
			t.Fatalf("%s: neither a section nor a setting: %q", f.Path, line)
		}
	}
	return values
}

// settings reads f, a strongSwan file of sections and key = value lines, into each value by its
// key's dotted path, as strongSwan's settings parser does. That parser reads the connection for
// swanctl, which passes it to charon through VICI, and the drop-in for charon.
func settings(t *testing.T, f File) map[string]string {
	t.Helper()
	read := map[string]string{}
	var sections []string
	for line := range strings.Lines(string(f.Data)) {
		line = strings.TrimSpace(line)
		key, value, isSetting := strings.Cut(line, "=")
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasSuffix(line, " {"):
			sections = append(sections, strings.TrimSuffix(line, " {"))
		case line == "}" && len(sections) > 0:
			sections = sections[:len(sections)-1]
		case isSetting:
			path := append(slices.Clone(sections), strings.TrimSpace(key))
			read[strings.Join(path, ".")] = strings.TrimSpace(value)
		default:
			t.Fatalf("%s: neither a section nor a setting: %q", f.Path, line)
		}
	}
	if len(sections) > 0 {
		t.Fatalf("%s: %q not closed", f.Path, sections)
	}
	return read
}

// charon makes no rule: routing_table 0 has it make none, nor route anything, which ikeward
// routing and updown do. The connection's updown names no network.
func TestGenerateNoRule(t *testing.T) {
	child := "connections." + Connection + ".children." + Connection + "."
	for name, p := range profiles {
		files, err := Generate(p, testSettings)
		if err != nil {
			t.Fatal(err)
		}
		dropIn := settings(t, files[1])
		if got, ok := dropIn["charon.routing_table"]; !ok || got != "0" {
			t.Errorf("%s: charon.routing_table is %q, want 0", name, got)
		}
		if got, ok := dropIn["charon.routing_table_prio"]; ok {
			t.Errorf("%s: charon.routing_table_prio is %q, where charon makes no rule", name, got)
		}
		if got := settings(t, files[0])[child+"updown"]; got != Binary+" updown" {
			t.Errorf("%s: the connection's updown is %q, want %s updown", name, got, Binary)
		}
	}
}
