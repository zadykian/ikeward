package profile

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// sswan is what ikeward reads of a .sswan, the JSON profile of strongSwan's Android app
// (docs.strongswan.org/docs/latest/os/androidVpnClientProfiles.html). It ignores the rest, such as
// local.rsa-pss, which ikev2setup.sh writes as the string "true".
type sswan struct {
	Type   string `json:"type"`
	Remote struct {
		Addr string          `json:"addr"`
		Port json.RawMessage `json:"port"`
		ID   string          `json:"id"`
		Cert string          `json:"cert"`
	} `json:"remote"`
	Local struct {
		ID string `json:"id"`
	} `json:"local"`
	IKEProposal string `json:"ike-proposal"`
	ESPProposal string `json:"esp-proposal"`
}

var (
	// hostName is a DNS name: labels of letters, digits and inner hyphens, joined by dots.
	hostName = regexp.MustCompile(
		`^([A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?\.)*[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$`)
	// proposals is strongSwan's syntax: algorithms joined by hyphens, proposals by commas.
	proposals = regexp.MustCompile(`^[A-Za-z0-9_]+([-,][A-Za-z0-9_]+)*$`)
)

// readSSwan sets p's server, identities and proposals from the .sswan f, and returns its CA, which
// is nil where it names none. What it takes goes into swanctl.conf, so it refuses what would not
// fit there.
func (p *Profile) readSSwan(f member) (*x509.Certificate, error) {
	var s sswan
	if err := json.Unmarshal(f.data, &s); err != nil {
		return nil, fmt.Errorf("%s: not the JSON of a .sswan: %w", f.name, err)
	}
	if s.Type != "ikev2-cert" {
		return nil, fmt.Errorf("%s: the type is %q, and ikeward sets up only ikev2-cert, "+
			"a login by certificate", f.name, s.Type)
	}
	// An IPv6 zone is refused: swanctl.conf gives % a meaning of its own.
	if addr, err := netip.ParseAddr(s.Remote.Addr); (err != nil || addr.Zone() != "") &&
		!hostName.MatchString(s.Remote.Addr) {
		return nil, fmt.Errorf("%s: remote.addr %q is neither a host name nor an address",
			f.name, s.Remote.Addr)
	}
	port, err := sswanPort(s.Remote.Port)
	if err != nil {
		return nil, fmt.Errorf("%s: remote.port %s is not a port", f.name, s.Remote.Port)
	}
	p.Server, p.Port, p.RemoteID, p.LocalID = s.Remote.Addr, port, s.Remote.ID, s.Local.ID
	if p.RemoteID == "" {
		p.RemoteID = p.Server
	}
	for _, id := range [][2]string{{"remote.id", p.RemoteID}, {"local.id", p.LocalID}} {
		if strings.ContainsFunc(id[1], badInID) {
			return nil, fmt.Errorf("%s: %s %q holds a character an identity cannot",
				f.name, id[0], id[1])
		}
	}
	p.IKEProposals, p.ESPProposals = s.IKEProposal, s.ESPProposal
	for _, proposal := range [][2]string{
		{"ike-proposal", p.IKEProposals}, {"esp-proposal", p.ESPProposals},
	} {
		if !proposals.MatchString(proposal[1]) {
			return nil, fmt.Errorf("%s: %s %q is not in strongSwan's syntax for proposals",
				f.name, proposal[0], proposal[1])
		}
	}
	return remoteCA(f.name, s.Remote.Cert)
}

// badInID is a character that swanctl.conf cannot take in a quoted identity.
func badInID(r rune) bool {
	return !unicode.IsPrint(r) || r == '"' || r == '\\'
}

// sswanPort reads remote.port: 500 where missing, else a number or, as the Android app also
// takes it, a string of one.
func sswanPort(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 500, nil
	}
	port, err := strconv.Atoi(strings.Trim(string(raw), `"`))
	if err == nil && (port < 1 || port > 65535) {
		err = strconv.ErrRange
	}
	return port, err
}

// remoteCA reads remote.cert, a certificate in Base64, which strongSwan's app checks the server
// by. It holds the server's CA, or the server's own certificate, which ikeward refuses rather than
// drop the pin.
func remoteCA(name, text string) (*x509.Certificate, error) {
	if text == "" {
		return nil, nil
	}
	data, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("%s: remote.cert is not Base64: %w", name, err)
	}
	cert, err := parseCertificate(data)
	if err != nil {
		return nil, fmt.Errorf("%s: remote.cert: %w", name, err)
	}
	if !isCA(cert) {
		return nil, fmt.Errorf("%s: remote.cert pins the server's certificate, %s, and ikeward "+
			"checks the server only by its CA", name, cert.Subject)
	}
	return cert, nil
}
