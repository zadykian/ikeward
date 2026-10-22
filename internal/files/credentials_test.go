package files

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"io"
	"testing"

	"github.com/zadykian/ikeward/internal/profile"
	"github.com/zadykian/ikeward/internal/profile/profiletest"
)

// The credentials are the profile's, in PEM, under the names the connection gives them, the key
// for root alone.
func TestCredentials(t *testing.T) {
	p, err := profile.Parse(profiletest.Files(t, "a_client.2", ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Credentials(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		path string
		mode uint32
		kind string
		der  []byte
	}{
		{"/etc/swanctl/x509/a_client.2-cert.pem", 0o644, "CERTIFICATE", p.Certificate.Raw},
		{"/etc/swanctl/x509ca/ikeward-ca.pem", 0o644, "CERTIFICATE", p.CA.Raw},
		{"/etc/swanctl/private/a_client.2-key.pem", 0o600, "PRIVATE KEY", nil},
	}
	if len(files) != len(want) {
		t.Fatalf("%d files, want %d", len(files), len(want))
	}
	for i, w := range want {
		f := files[i]
		if f.Path != w.path || uint32(f.Mode) != w.mode {
			t.Errorf("file %d: %s, mode %04o; want %s, mode %04o", i, f.Path, f.Mode, w.path, w.mode)
		}
		block, rest := pem.Decode(f.Data)
		if block == nil || block.Type != w.kind || len(rest) > 0 {
			t.Errorf("%s is not one PEM block of %s:\n%s", f.Path, w.kind, f.Data)
			continue
		}
		if w.der == nil {
			checkKey(t, f.Path, block.Bytes, p.Certificate)
		} else if !bytes.Equal(block.Bytes, w.der) {
			t.Errorf("%s holds another certificate", f.Path)
		}
	}
	// The connection names the same certificate.
	generated, err := Generate(p, testSettings)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(generated[0].Data, []byte("certs = a_client.2-cert.pem\n")) {
		t.Errorf("the connection names another certificate:\n%s", generated[0].Data)
	}
}

// checkKey fails t unless der, from the file at path, is the PKCS #8 key of cert.
func checkKey(t *testing.T, path string, der []byte, cert *x509.Certificate) {
	t.Helper()
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	signer, ok := key.(crypto.Signer)
	public, okCert := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !okCert || !public.Equal(signer.Public()) {
		t.Errorf("%s does not hold the certificate's key", path)
	}
}

// opaqueKey is a key that x509 cannot write.
type opaqueKey struct{ ed25519.PrivateKey }

func (opaqueKey) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) { return nil, nil }

func TestCredentialsErrors(t *testing.T) {
	p, err := profile.Parse(profiletest.Files(t, "client", ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	lacking := "cannot write the credentials: the profile lacks them"
	for _, test := range []struct {
		name string
		p    *profile.Profile
		want string
	}{
		{"no profile", nil, lacking},
		{"no key", &profile.Profile{Name: "client", Certificate: p.Certificate, CA: p.CA}, lacking},
		{"no certificate", &profile.Profile{Name: "client", Key: p.Key, CA: p.CA}, lacking},
		{"no CA", &profile.Profile{Name: "client", Key: p.Key, Certificate: p.Certificate}, lacking},
		{"opaque key", &profile.Profile{Name: "client", Key: opaqueKey{}, Certificate: p.Certificate,
			CA: p.CA}, "cannot write the key of client.p12: x509: unknown key type while marshaling " +
			"PKCS#8: files.opaqueKey"},
	} {
		files, err := Credentials(test.p)
		if err == nil || files != nil || err.Error() != test.want {
			t.Errorf("%s: %d files, %v; want %s", test.name, len(files), err, test.want)
		}
	}
}
