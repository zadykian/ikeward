// Package files generates from the client profile the files that install writes, with no I/O:
//
//   - the swanctl connection, Connection, whose name its file and its CA's file take too. Its
//     cacerts let only the profile's CA sign the server's certificate. Its remote_ts is every
//     address, which the VPN server narrows to the remote network's ranges, and its updown,
//     ikeward updown, routes each of them through Link;
//   - one strongSwan drop-in, in which charon makes no rule of its own, and retries with
//     retry_initiate_interval as the connection does with keyingtries. The package's
//     resolve.conf stays as shipped;
//   - networkd's files of Link, an XFRM interface whose ID the SAs take. Its own address,
//     LinkULA, which charon ignores, keeps the remote network's names on it;
//   - the watchdog's service, which runs ikeward watchdog, and its timer;
//   - ikeward routing's service, and the file in networkd.conf.d;
//   - SettingsFile, the remote network's values, which install's flags give.
package files

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"text/template"

	"github.com/zadykian/ikeward/internal/profile"
)

// The names and addresses that the files set, and status checks.
const (
	// Binary is where install.sh puts ikeward, and where strongSwan and systemd run it.
	Binary = "/usr/local/sbin/ikeward"
	// Connection names the swanctl connection, its child and its file in conf.d.
	Connection = "ikeward"
	// CACert is the profile's CA in /etc/swanctl/x509ca: the only CA the connection accepts the
	// server's certificate from.
	CACert = Connection + "-ca.pem"
	// Link is the tunnel's XFRM interface, which networkd makes: the connection's SAs take
	// InterfaceID, and the address the server assigns goes on it. Without an SA, it drops what it
	// would send, rather than send it in the clear.
	Link = "ikeward0"
	// InterfaceID is Link's ID, which the SAs and their policies take: "ik" in ASCII, which no
	// other XFRM interface on the server is likely to take.
	InterfaceID = 0x696b
	// LinkULA is Link's own address, from a unique local prefix with a random global ID, as RFC
	// 4193 says. Without a routable address on Link, resolved sends the remote network's names
	// elsewhere. Being IPv6, it takes no part in the choice of their IPv4 source, as an IPv4 one
	// would. With it, they stay on Link while the tunnel is down, and fail as docs/status.md says.
	LinkULA = "fd22:851f:35dd::1/128"
	// Table is the routing table that routes the remote network's ranges through Link, as ikeward
	// updown sets them, and holds the floors, as ikeward routing sets them. A rule of ikeward
	// routing's looks it up after the main table's own networks.
	Table = 220
	// Watchdog names the watchdog's service and its timer.
	Watchdog = "ikeward-watchdog"
	// Routing names the service that runs ikeward routing, before networkd and strongSwan.
	Routing = "ikeward-routing"
	// StrongSwan is strongSwan's unit, which charon-systemd ships, and the watchdog's service
	// runs after.
	StrongSwan = "strongswan.service"
)

// File is a file that install writes.
type File struct {
	// Path is where the file goes, an absolute path.
	Path string
	// Mode is its permission bits.
	Mode fs.FileMode
	// Data is what it holds.
	Data []byte
}

// fields are what the templates read: the constants above, the settings, and the profile's values
// for the connection. They are copies, so that no template can reach the profile's private key.
type fields struct {
	Header, Binary, Connection, CACert, Link, LinkULA, Watchdog, Routing, SettingsFile string
	Name, Server, RemoteID, LocalID, IKEProposals, ESPProposals                        string
	Table, Port, InterfaceID                                                           int
	Settings                                                                           Settings
}

// header starts each file, which install writes again where it differs.
const header = "# Written by ikeward install, which rewrites it: change ikeward, not this file."

// Generate returns the files for the profile p and the settings s, in a fixed order. The
// connection comes first, then the strongSwan drop-in, networkd's files, the watchdog's units, the
// routing's and SettingsFile. Settings that Check refuses fail it.
func Generate(p *profile.Profile, s Settings) ([]File, error) {
	if p == nil {
		return nil, errors.New("cannot generate the files: no profile")
	}
	if err := s.Check(); err != nil {
		return nil, fmt.Errorf("cannot generate the files: %w", err)
	}
	in := fields{
		Header: header, Binary: Binary, Connection: Connection, CACert: CACert, Link: Link,
		LinkULA: LinkULA, Watchdog: Watchdog, Routing: Routing, SettingsFile: SettingsFile,
		Name: p.Name, Server: p.Server, RemoteID: p.RemoteID, LocalID: p.LocalID,
		IKEProposals: p.IKEProposals, ESPProposals: p.ESPProposals,
		Table: Table, Port: p.Port, InterfaceID: InterfaceID, Settings: s,
	}
	files := make([]File, 0, len(templates))
	for _, t := range templates {
		var out bytes.Buffer
		if err := t.Execute(&out, in); err != nil {
			return nil, fmt.Errorf("cannot generate %s: %w", t.Name(), err)
		}
		files = append(files, File{Path: t.Name(), Mode: 0o644, Data: out.Bytes()})
	}
	return files, nil
}

// templates are the files, each named by its path, in the order Generate returns them.
var templates = func() []*template.Template {
	funcs := template.FuncMap{"value": value, "join": strings.Join}
	var parsed []*template.Template
	for _, f := range []struct{ path, text string }{
		{"/etc/swanctl/conf.d/" + Connection + ".conf", connectionText},
		{"/etc/strongswan.d/zz-ikeward.conf", dropInText},
		{"/etc/systemd/network/25-" + Link + ".netdev", netdevText},
		{"/etc/systemd/network/25-" + Link + ".network", networkText},
		{"/etc/systemd/networkd.conf.d/10-keep-vpn-routing.conf", keepRoutingText},
		{"/etc/systemd/system/" + Watchdog + ".service", serviceText},
		{"/etc/systemd/system/" + Watchdog + ".timer", timerText},
		{"/etc/systemd/system/" + Routing + ".service", routingText},
		{SettingsFile, settingsText},
	} {
		t := template.New(f.path).Funcs(funcs).Option("missingkey=error")
		parsed = append(parsed, template.Must(t.Parse(f.text)))
	}
	return parsed
}()

// word is a value that strongSwan's parser reads as one word, unquoted.
var word = regexp.MustCompile(`^[A-Za-z0-9._:@/,=-]+$`)

// escapes writes what a quoted value holds as strongSwan's parser reads it: a backslash or a quote
// escaped, and control characters as C writes them. The parser drops a bare carriage return.
var escapes = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

// value is s as a value in swanctl.conf: s itself where s is one plain word, else s in double
// quotes. Unquoted, strongSwan's parser joins words with one space, and #, } and " end a value.
func value(s string) string {
	if word.MatchString(s) {
		return s
	}
	return `"` + escapes.Replace(s) + `"`
}
