// Package profile reads the client profile: NAME.p12, NAME.sswan and NAME.mobileconfig, the files
// that ikev2setup.sh writes, the last optional.
//
// The .sswan gives the server and the proposals. The .p12 gives the client's key and certificate.
// The CA that issued the certificate comes from the .p12 where it holds it, else from the .sswan's
// remote.cert, else from the .mobileconfig's root payload. A .p12 with a password asks for it. The
// package reads no file itself: the caller hands it each file's name and bytes.
package profile

import (
	"crypto"
	"crypto/x509"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Profile is what ikeward takes from the client profile.
type Profile struct {
	// Name is NAME of NAME.p12, NAME.sswan and NAME.mobileconfig; it names the files install writes.
	Name string
	// Server is the VPN server's host name or address, and Port its IKE port.
	Server string
	Port   int
	// RemoteID is the server's identity, Server unless the .sswan sets one. LocalID is the
	// client's, empty for the certificate's subject.
	RemoteID, LocalID string
	// IKEProposals and ESPProposals are in strongSwan's syntax, as swanctl.conf takes them.
	IKEProposals, ESPProposals string
	// Key and Certificate are the client's, and CA the certificate that issued Certificate.
	Key         crypto.Signer
	Certificate *x509.Certificate
	CA          *x509.Certificate
	// Password is whether the .p12 has a password, and CAFile the file CA came from.
	Password bool
	CAFile   string
}

// Password gives the .p12's password. Parse calls it at most once, when the .p12 has a password.
type Password func() (string, error)

// MaxSize caps each of the profile's files: an issued file is a few kilobytes.
const MaxSize = 1 << 20

// safeName is a name install can build file names on, such as NAME-cert.pem.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// File is one of the profile's files: its base name, which errors and the summary give, and its
// bytes.
type File struct {
	Name string
	Data []byte
}

// stem is f's name without its extension.
func (f File) stem() string {
	return strings.TrimSuffix(f.Name, path.Ext(f.Name))
}

// Files are the profile's files. Mobileconfig is optional, with no name where none was given, and
// Parse reads it only where neither of the others holds the CA.
type Files struct {
	P12, SSwan, Mobileconfig File
}

// Parse reads the client profile from files. password may be nil where the .p12 has no password.
func Parse(files Files, password Password) (*Profile, error) {
	if err := files.check(); err != nil {
		return nil, err
	}
	p := &Profile{Name: files.P12.stem()}
	if !safeName.MatchString(p.Name) {
		return nil, fmt.Errorf("%s: %q is not a name ikeward builds file names on",
			files.P12.Name, p.Name)
	}
	remote, err := p.readSSwan(files.SSwan)
	if err != nil {
		return nil, err
	}
	chain, err := p.readP12(files.P12, password)
	if err != nil {
		return nil, err
	}
	if err := p.findCA(chain, remote, files); err != nil {
		return nil, err
	}
	return p, nil
}

// Summary describes p in lines that hold no secret, for install to print.
func (p *Profile) Summary() []string {
	password := "no password"
	if p.Password {
		password = "a password"
	}
	lines := []string{
		fmt.Sprintf("server %s, port %d, identity %s", p.Server, p.Port, p.RemoteID),
		fmt.Sprintf("proposals IKE %s, ESP %s", p.IKEProposals, p.ESPProposals),
		fmt.Sprintf("certificate %s, valid until %s, from %s.p12, which has %s",
			p.Certificate.Subject, p.Certificate.NotAfter.Format("2006-01-02"), p.Name, password),
		fmt.Sprintf("CA %s, valid until %s, from %s",
			p.CA.Subject, p.CA.NotAfter.Format("2006-01-02"), p.CAFile),
	}
	if p.LocalID != "" {
		lines = append(lines, "client identity "+p.LocalID)
	}
	return lines
}

// check refuses a file over MaxSize bytes, and files that are not named alike, as two profiles'
// would be.
func (f Files) check() error {
	for _, file := range []File{f.P12, f.SSwan, f.Mobileconfig} {
		if len(file.Data) > MaxSize {
			return fmt.Errorf("%s: over %d bytes, more than an issued file holds", file.Name,
				MaxSize)
		}
	}
	for _, other := range []File{f.SSwan, f.Mobileconfig} {
		if other.Name != "" && other.stem() != f.P12.stem() {
			return fmt.Errorf("%s and %s are not named alike, as one profile's files are",
				f.P12.Name, other.Name)
		}
	}
	return nil
}
