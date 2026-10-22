package profile

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// testPKI is a CA and a client's key and certificate it issued, all made for a test.
type testPKI struct {
	caKey, key crypto.Signer
	ca, cert   *x509.Certificate
}

// newPKI makes a CA and a client certificate as ikev2setup.sh names them, with keys newKey makes.
func newPKI(t *testing.T, newKey func(*testing.T) crypto.Signer) testPKI {
	t.Helper()
	pki := testPKI{caKey: newKey(t), key: newKey(t)}
	pki.ca = newCA(t, "Test VPN CA", pki.caKey)
	client := &x509.Certificate{
		Subject:     pkix.Name{CommonName: "client", Organization: []string{"Test VPN"}},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	pki.cert = sign(t, client, pki.key.Public(), pki.ca, pki.caKey)
	return pki
}

// newCA makes a self-signed CA named cn with key.
func newCA(t *testing.T, cn string, key crypto.Signer) *x509.Certificate {
	t.Helper()
	ca := &x509.Certificate{
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"Test VPN"}},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	return sign(t, ca, key.Public(), ca, key)
}

// sign issues template for public, signed by parent's key.
func sign(t *testing.T, template *x509.Certificate, public crypto.PublicKey,
	parent *x509.Certificate, parentKey crypto.Signer) *x509.Certificate {
	t.Helper()
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	template.SerialNumber = serial
	template.NotBefore = time.Date(2025, 1, 29, 0, 0, 0, 0, time.UTC)
	template.NotAfter = time.Date(2035, 1, 29, 0, 0, 0, 0, time.UTC)
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

func ecKey(t *testing.T) crypto.Signer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func rsaKey(t *testing.T) crypto.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// encodeP12 makes a .p12 of key, cert and chain with encoder and password.
func encodeP12(t *testing.T, encoder *pkcs12.Encoder, key any, cert *x509.Certificate,
	chain []*x509.Certificate, password string) []byte {
	t.Helper()
	data, err := encoder.Encode(key, cert, chain, password)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// sswanText is a .sswan as ikev2setup.sh writes it; %s is the .p12 in Base64.
const sswanText = `{
  "uuid": "1b4e28ba-2fa1-11d2-883f-0016d3cca427",
  "name": "IKEv2 VPN vpn.example.org",
  "type": "ikev2-cert",
  "remote": {
    "addr": "vpn.example.org"
  },
  "local": {
    "p12": "%s",
    "rsa-pss": "true"
  },
  "ike-proposal": "aes256-sha256-modp2048",
  "esp-proposal": "aes128gcm16"
}
`

// newSSwan makes a .sswan for p12, which change, if not nil, edits as a JSON object first.
func newSSwan(t *testing.T, p12 []byte, change func(map[string]any)) []byte {
	t.Helper()
	data := fmt.Appendf(nil, sswanText, wrap(base64.StdEncoding.EncodeToString(p12), `\n`))
	if change == nil {
		return data
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	change(object)
	data, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// wrap splits text into lines of 52 characters joined by newline, as ikev2setup.sh does.
func wrap(text, newline string) string {
	var lines []string
	for len(text) > 52 {
		lines = append(lines, text[:52])
		text = text[52:]
	}
	return strings.Join(append(lines, text), newline)
}

// newMobileconfig makes a .mobileconfig as ikev2setup.sh writes it, with a root payload for each
// of roots, the bytes of a certificate.
func newMobileconfig(roots ...[]byte) []byte {
	var payloads strings.Builder
	for _, root := range roots {
		fmt.Fprintf(&payloads, mobileconfigRoot, plistData(root))
	}
	return fmt.Appendf(nil, mobileconfigText, payloads.String())
}

// plistData is data as a plist's <data> holds it: Base64 in indented lines.
func plistData(data []byte) string {
	return "\n\t\t\t" + wrap(base64.StdEncoding.EncodeToString(data), "\n\t\t\t") + "\n\t\t\t"
}

// mobileconfigText is a .mobileconfig; %s is its root payloads.
const mobileconfigText = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>PayloadContent</key>
  <array>
    <dict>
      <key>IKEv2</key>
      <dict>
        <key>AuthenticationMethod</key>
        <string>Certificate</string>
        <key>ChildSecurityAssociationParameters</key>
        <dict>
          <key>DiffieHellmanGroup</key>
          <integer>19</integer>
          <key>EncryptionAlgorithm</key>
          <string>AES-256-GCM</string>
        </dict>
        <key>DisableMOBIKE</key>
        <integer>0</integer>
        <key>EnablePFS</key>
        <false/>
        <key>OnDemandEnabled</key>
        <true/>
        <key>OnDemandRules</key>
        <array>
          <dict>
            <key>Action</key>
            <string>Connect</string>
          </dict>
        </array>
        <key>RemoteAddress</key>
        <string>vpn.example.org</string>
      </dict>
      <key>PayloadType</key>
      <string>com.apple.vpn.managed</string>
      <key>VPNType</key>
      <string>IKEv2</string>
    </dict>
    <dict>
      <key>Password</key>
      <string>not the .p12's</string>
      <key>PayloadContent</key>
      <data>
      bm90IGEgLnAxMg==
      </data>
      <key>PayloadType</key>
      <string>com.apple.security.pkcs12</string>
    </dict>%s
  </array>
  <key>PayloadDisplayName</key>
  <string>IKEv2 VPN vpn.example.org</string>
  <key>PayloadType</key>
  <string>Configuration</string>
  <key>PayloadVersion</key>
  <integer>1</integer>
</dict>
</plist>
`

// mobileconfigRoot is a root payload; %s is its certificate as plistData gives it.
const mobileconfigRoot = `
    <dict>
      <key>PayloadCertificateFileName</key>
      <string>ikev2vpnca</string>
      <key>PayloadContent</key>
      <data>%s</data>
      <key>PayloadType</key>
      <string>com.apple.security.root</string>
    </dict>`
