// Package profile reads the client profile: a zip that holds NAME.p12, NAME.sswan,
// NAME.mobileconfig and scripts for other systems, which ikeward ignores.
//
// The .sswan gives the server and the proposals. The .p12 gives the client's key and certificate.
// The CA that issued the certificate comes from the .p12 where it holds it, else from the .sswan's
// remote.cert, else from the .mobileconfig's root payload. A .p12 with a password asks for it. The
// package reads no file itself: the caller hands it the zip's bytes.
package profile

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/x509"
	"fmt"
	"io"
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

// maxSize caps each file read from the zip: an issued file is a few kilobytes.
const maxSize = 1 << 20

// safeName is a name install can build file names on, such as NAME-cert.pem.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// member is a file of the zip, named by its base name.
type member struct {
	name string
	data []byte
}

// stem is m's name without its extension.
func (m member) stem() string {
	return strings.TrimSuffix(m.name, path.Ext(m.name))
}

// Parse reads the client profile from data, the zip, which errors call name. password may be nil
// where the .p12 has no password.
func Parse(name string, data []byte, password Password) (*Profile, error) {
	files, err := unzip(name, data)
	if err != nil {
		return nil, err
	}
	p12 := files[".p12"]
	p := &Profile{Name: p12.stem()}
	if !safeName.MatchString(p.Name) {
		return nil, fmt.Errorf("%s: %s: %q is not a name ikeward builds file names on",
			name, p12.name, p.Name)
	}
	remote, err := p.readSSwan(files[".sswan"])
	if err != nil {
		return nil, err
	}
	chain, err := p.readP12(p12, password)
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

// unzip reads the zip's .p12, .sswan and .mobileconfig, the last optional, and skips the rest. It
// takes an extension in any case, as Windows may write it, and keys files by it in lower case.
func unzip(name string, data []byte) (map[string]member, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	files := map[string]member{}
	for _, f := range r.File {
		base := path.Base(f.Name)
		ext := strings.ToLower(path.Ext(base))
		// A zip made on macOS adds an AppleDouble file ._NAME beside each file.
		if f.FileInfo().IsDir() || strings.HasPrefix(base, "._") ||
			(ext != ".p12" && ext != ".sswan" && ext != ".mobileconfig") {
			continue
		}
		if other, ok := files[ext]; ok {
			return nil, fmt.Errorf("%s: holds two %s files, %s and %s", name, ext, other.name, base)
		}
		content, err := extract(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", name, f.Name, err)
		}
		files[ext] = member{base, content}
	}
	for _, ext := range []string{".p12", ".sswan"} {
		if _, ok := files[ext]; !ok {
			return nil, fmt.Errorf("%s: holds no %s file", name, ext)
		}
	}
	// The files of two profiles must not mix.
	p12 := files[".p12"]
	for _, ext := range []string{".sswan", ".mobileconfig"} {
		if f, ok := files[ext]; ok && f.stem() != p12.stem() {
			return nil, fmt.Errorf("%s: %s and %s are not named alike, as one profile's files are",
				name, p12.name, f.name)
		}
	}
	return files, nil
}

// extract reads f, failing where it holds over maxSize bytes.
func extract(f *zip.File) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if closeErr := r.Close(); err == nil {
		err = closeErr
	}
	if err == nil && len(data) > maxSize {
		err = fmt.Errorf("over %d bytes, more than an issued file holds", maxSize)
	}
	return data, err
}
