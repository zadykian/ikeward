package profile

import (
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"slices"
	"strings"
	"testing"

	"software.sslmate.com/src/go-pkcs12"
)

// issued is the files of a client profile, which a test changes before zip makes the profile.
type issued struct {
	t                        *testing.T
	pki                      testPKI
	p12, sswan, mobileconfig []byte // The zip holds no .mobileconfig where mobileconfig is nil.
}

// newIssued makes a profile as ikev2setup.sh issues one. Its .p12 has no password, is in OpenSSL's
// -legacy 3DES, and holds the CA, which the .mobileconfig's root payload also holds.
func newIssued(t *testing.T, pki testPKI) *issued {
	t.Helper()
	p12 := encodeP12(t, pkcs12.LegacyDES, pki.key, pki.cert, []*x509.Certificate{pki.ca}, "")
	return &issued{t, pki, p12, newSSwan(t, p12, nil), newMobileconfig(pki.ca.Raw)}
}

// setP12 replaces the .p12 with one of the client's key and cert, chain and password.
func (i *issued) setP12(encoder *pkcs12.Encoder, cert *x509.Certificate,
	chain []*x509.Certificate, password string) {
	i.p12 = encodeP12(i.t, encoder, i.pki.key, cert, chain, password)
}

// setRemote sets the .sswan's remote.cert to cert.
func (i *issued) setRemote(cert *x509.Certificate) {
	i.sswan = newSSwan(i.t, i.p12, remote("cert", base64.StdEncoding.EncodeToString(cert.Raw)))
}

// caLike makes a CA with the name of i's, and key, from a template change edits if not nil.
func caLike(i *issued, key crypto.Signer, change func(*x509.Certificate)) *x509.Certificate {
	i.t.Helper()
	ca := &x509.Certificate{
		Subject:               i.pki.ca.Subject,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	if change != nil {
		change(ca)
	}
	return sign(i.t, ca, key.Public(), ca, key)
}

// zip makes the profile, scripts for other systems among its files.
func (i *issued) zip() []byte {
	files := []file{
		{"client.p12", i.p12}, {"client.sswan", i.sswan},
		{"mac-os.sh", []byte("#!/bin/sh\n")}, {"windows.cmd", []byte("@echo off\r\n")},
	}
	if i.mobileconfig != nil {
		files = append(files, file{"client.mobileconfig", i.mobileconfig})
	}
	return zipOf(i.t, files...)
}

// noPassword fails the test where Parse asks for a password.
func noPassword(t *testing.T) Password {
	t.Helper()
	return func() (string, error) {
		t.Error("Parse asked for a password")
		return "", nil
	}
}

// parseTests are profiles that Parse reads, each a change to one as ikev2setup.sh issues it.
var parseTests = []struct {
	name     string
	change   func(*issued)
	password string // What Password gives, where the .p12 has a password.
	caFile   string
}{
	{"as ikev2setup.sh issues it", func(*issued) {}, "", "client.p12"},
	{"without a .mobileconfig", func(i *issued) { i.mobileconfig = nil }, "", "client.p12"},
	{"with RSA keys", func(i *issued) { *i = *newIssued(i.t, newPKI(i.t, rsaKey)) }, "", "client.p12"},
	{"with a password", func(i *issued) {
		i.setP12(pkcs12.Modern2023, i.pki.cert, []*x509.Certificate{i.pki.ca}, "pass word")
	}, "pass word", "client.p12"},
	{"with a password and PBMAC1", func(i *issued) {
		i.setP12(pkcs12.Modern2026, i.pki.cert, []*x509.Certificate{i.pki.ca}, "pass word")
	}, "pass word", "client.p12"},
	{"without encryption", func(i *issued) {
		i.setP12(pkcs12.Passwordless, i.pki.cert, []*x509.Certificate{i.pki.ca}, "")
	}, "", "client.p12"},
	{"with the CA before the client's certificate", func(i *issued) {
		i.setP12(pkcs12.LegacyDES, i.pki.ca, []*x509.Certificate{i.pki.cert}, "")
	}, "", "client.p12"},
	{"with the CA in the .mobileconfig only", func(i *issued) {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, nil, "")
	}, "", "client.mobileconfig"},
	{"with the CA in the .mobileconfig only, as PEM", func(i *issued) {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, nil, "")
		i.mobileconfig = newMobileconfig(
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: i.pki.ca.Raw}))
	}, "", "client.mobileconfig"},
	{"with a certificate signed with SHA-1", func(i *issued) {
		pki := newPKI(i.t, rsaKey)
		client := &x509.Certificate{
			Subject:            pkix.Name{CommonName: "client", Organization: []string{"Test VPN"}},
			SignatureAlgorithm: x509.SHA1WithRSA,
		}
		pki.cert = sign(i.t, client, pki.key.Public(), pki.ca, pki.caKey)
		*i = *newIssued(i.t, pki)
	}, "", "client.p12"},
	{"with the CA in the .sswan's remote.cert only", func(i *issued) {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, nil, "")
		i.setRemote(i.pki.ca)
		i.mobileconfig = nil
	}, "", "client.sswan"},
	{"with the CA in the .p12 and in remote.cert", func(i *issued) { i.setRemote(i.pki.ca) },
		"", "client.p12"},
	{"with a CA of the same name in the .p12, and the CA in the .mobileconfig", func(i *issued) {
		other := caLike(i, ecKey(i.t), nil)
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{other}, "")
	}, "", "client.mobileconfig"},
	{"with another CA in the .p12, and the CA after it in the .mobileconfig", func(i *issued) {
		other := newCA(i.t, "Other CA", ecKey(i.t))
		i.setP12(pkcs12.Modern2023, i.pki.cert, []*x509.Certificate{other}, "pass word")
		i.mobileconfig = newMobileconfig(other.Raw, i.pki.ca.Raw)
	}, "pass word", "client.mobileconfig"},
}

func TestParse(t *testing.T) {
	for _, test := range parseTests {
		t.Run(test.name, func(t *testing.T) {
			i := newIssued(t, newPKI(t, ecKey))
			test.change(i)
			password, asked := noPassword(t), 0
			if test.password != "" {
				password = func() (string, error) {
					asked++
					return test.password, nil
				}
			}
			p, err := Parse("profile.zip", i.zip(), password)
			if err != nil {
				t.Fatal(err)
			}
			if test.password != "" && (!p.Password || asked != 1) {
				t.Errorf("Password %t, asked %d times, want true and once", p.Password, asked)
			}
			checkProfile(t, p, i.pki, test.caFile)
		})
	}
}

// checkProfile checks what Parse read of a profile made of pki, with the CA from caFile.
func checkProfile(t *testing.T, p *Profile, pki testPKI, caFile string) {
	t.Helper()
	got := []any{p.Name, p.Server, p.Port, p.RemoteID, p.LocalID, p.IKEProposals, p.ESPProposals,
		p.CAFile}
	want := []any{"client", "vpn.example.org", 500, "vpn.example.org", "", "aes256-sha256-modp2048",
		"aes128gcm16", caFile}
	if !slices.Equal(got, want) {
		t.Errorf("Parse read %q, want %q", got, want)
	}
	if !matches(p.Key, pki.cert) || !p.Certificate.Equal(pki.cert) {
		t.Errorf("Parse read the certificate %s, or its key, not the client's", p.Certificate.Subject)
	}
	if !p.CA.Equal(pki.ca) {
		t.Errorf("Parse read the CA %s, not the one that issued the client's", p.CA.Subject)
	}
}

func TestSummary(t *testing.T) {
	i := newIssued(t, newPKI(t, ecKey))
	p, err := Parse("profile.zip", i.zip(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"server vpn.example.org, port 500, identity vpn.example.org",
		"proposals IKE aes256-sha256-modp2048, ESP aes128gcm16",
		"certificate CN=client,O=Test VPN, valid until 2035-01-29, from client.p12, " +
			"which has no password",
		"CA CN=Test VPN CA,O=Test VPN, valid until 2035-01-29, from client.p12",
	}
	if got := p.Summary(); !slices.Equal(got, want) {
		t.Errorf("Summary:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	p.Password, p.CAFile, p.LocalID = true, "client.mobileconfig", "client@example.org"
	want[2] = strings.Replace(want[2], "no password", "a password", 1)
	want[3] = strings.Replace(want[3], "client.p12", "client.mobileconfig", 1)
	want = append(want, "client identity client@example.org")
	if got := p.Summary(); !slices.Equal(got, want) {
		t.Errorf("Summary:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestZip checks which of the zip's files Parse reads: by their extension, in any directory.
func TestZip(t *testing.T) {
	i := newIssued(t, newPKI(t, ecKey))
	data := zipOf(t,
		file{"client/", nil},
		file{"client/client.p12", i.p12},
		file{"client/client.sswan", i.sswan},
		file{"client/client.mobileconfig", i.mobileconfig},
		file{"__MACOSX/client/._client.p12", []byte("AppleDouble")},
		file{"client/README.txt", []byte("not read")},
	)
	p, err := Parse("profile.zip", data, noPassword(t))
	if err != nil {
		t.Fatal(err)
	}
	checkProfile(t, p, i.pki, "client.p12")
}

// TestZipExtensionCase checks that Parse takes the files' extensions in any case.
func TestZipExtensionCase(t *testing.T) {
	i := newIssued(t, newPKI(t, ecKey))
	data := zipOf(t,
		file{"client.P12", i.p12},
		file{"client.SSWAN", i.sswan},
		file{"client.MobileConfig", i.mobileconfig},
	)
	p, err := Parse("profile.zip", data, noPassword(t))
	if err != nil {
		t.Fatal(err)
	}
	checkProfile(t, p, i.pki, "client.P12")
}
