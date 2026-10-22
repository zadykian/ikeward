// Package profiletest makes client profiles for the tests of other packages, from a CA and a
// client's certificate made for the test: no real profile goes in the repository.
package profiletest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"github.com/zadykian/ikeward/internal/profile"
)

// Server is the VPN server that the profiles name, in the .sswan's remote.addr.
const Server = "vpn.example.org"

// sswanText is a .sswan as ikev2setup.sh writes it; %[1]s is the server, %[2]s the .p12 in
// Base64.
const sswanText = `{
  "uuid": "1b4e28ba-2fa1-11d2-883f-0016d3cca427",
  "name": "IKEv2 VPN %[1]s",
  "type": "ikev2-cert",
  "remote": {
    "addr": "%[1]s"
  },
  "local": {
    "p12": "%[2]s",
    "rsa-pss": "true"
  },
  "ike-proposal": "aes256-sha256-modp2048",
  "esp-proposal": "aes128gcm16"
}
`

// CA is a CA made for a test, named as ikev2setup.sh names its own. It issues the client's
// certificate in the profiles it makes, and a server's, for a test that runs one.
type CA struct {
	// Cert is the CA's certificate.
	Cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// NewCA makes a CA.
func NewCA(t *testing.T) *CA {
	t.Helper()
	key := newKey(t)
	cert := sign(t, &x509.Certificate{
		Subject:               pkix.Name{CommonName: "Test VPN CA", Organization: []string{"Test VPN"}},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}, &key.PublicKey, nil, key)
	return &CA{Cert: cert, key: key}
}

// Files returns a client profile's files as ikev2setup.sh makes them, from a CA of their own: see
// CA.Files.
func Files(t *testing.T, name, password string) profile.Files {
	t.Helper()
	return NewCA(t).Files(t, name, password)
}

// Files returns a client profile's files as ikev2setup.sh makes them. NAME.p12 holds the client's
// key, a certificate that ca issued for it, and ca's own; NAME.sswan is for Server. The .p12 has
// password, or none where password is empty.
func (ca *CA) Files(t *testing.T, name, password string) profile.Files {
	t.Helper()
	key := newKey(t)
	cert := sign(t, &x509.Certificate{
		Subject:     pkix.Name{CommonName: name, Organization: []string{"Test VPN"}},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}, &key.PublicKey, ca.Cert, ca.key)
	p12, err := pkcs12.Modern2023.Encode(key, cert, []*x509.Certificate{ca.Cert}, password)
	if err != nil {
		t.Fatal(err)
	}
	sswan := fmt.Appendf(nil, sswanText, Server, base64.StdEncoding.EncodeToString(p12))
	return profile.Files{
		P12:   profile.File{Name: name + ".p12", Data: p12},
		SSwan: profile.File{Name: name + ".sswan", Data: sswan},
	}
}

// Server returns, in PEM, a certificate that ca issued to the VPN server Server, and its key as
// PKCS #8. The certificate holds the name as a DNS name too, as strongSwan matches the server's
// identity, the name, against it.
func (ca *CA) Server(t *testing.T) (cert, key []byte) {
	t.Helper()
	serverKey := newKey(t)
	issued := sign(t, &x509.Certificate{
		Subject:     pkix.Name{CommonName: Server, Organization: []string{"Test VPN"}},
		DNSNames:    []string{Server},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, &serverKey.PublicKey, ca.Cert, ca.key)
	der, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	return PEM(issued), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// PEM is cert in PEM.
func PEM(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// sign issues template for public, signed by parentKey as parent, or self-signed where parent is
// nil.
func sign(t *testing.T, template *x509.Certificate, public *ecdsa.PublicKey,
	parent *x509.Certificate, parentKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	template.SerialNumber = serial
	template.NotBefore = time.Date(2025, 1, 29, 0, 0, 0, 0, time.UTC)
	template.NotAfter = time.Date(2035, 1, 29, 0, 0, 0, 0, time.UTC)
	if parent == nil {
		parent = template
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, public, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
