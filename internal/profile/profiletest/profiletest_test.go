package profiletest_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/profile"
	"github.com/zadykian/ikeward/internal/profile/profiletest"
)

// TestCAZip reads a profile from a CA, as install does: the client's certificate is the CA's,
// and the server is Server.
func TestCAZip(t *testing.T) {
	ca := profiletest.NewCA(t)
	p, err := profile.Parse("client.zip", ca.Zip(t, "client", "secret"),
		func() (string, error) { return "secret", nil })
	if err != nil {
		t.Fatal(err)
	}
	if !p.CA.Equal(ca.Cert) {
		t.Errorf("the profile's CA is %s, not the test's", p.CA.Subject)
	}
	if err := p.Certificate.CheckSignatureFrom(ca.Cert); err != nil {
		t.Errorf("the client's certificate is not the CA's: %v", err)
	}
	if p.Server != profiletest.Server || p.RemoteID != profiletest.Server {
		t.Errorf("the profile names the server %s, as %s, not %s", p.Server, p.RemoteID,
			profiletest.Server)
	}
}

// TestCAServer checks the server's certificate as strongSwan's client does: from the CA, for
// Server's name, with the key that goes with it.
func TestCAServer(t *testing.T) {
	ca := profiletest.NewCA(t)
	certPEM, keyPEM := ca.Server(t)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("the certificate and the key do not go together: %v", err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, err := cert.Verify(x509.VerifyOptions{
		DNSName:     profiletest.Server,
		Roots:       roots,
		CurrentTime: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Error(err)
	}
	if block, _ := pem.Decode(keyPEM); block == nil || block.Type != "PRIVATE KEY" {
		t.Errorf("the key is not PKCS #8 in PEM:\n%s", keyPEM)
	}
	if block, _ := pem.Decode(profiletest.PEM(ca.Cert)); block == nil ||
		block.Type != "CERTIFICATE" || string(block.Bytes) != string(ca.Cert.Raw) {
		t.Error("PEM does not hold the CA's certificate")
	}
}
