package profile

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"strings"
	"testing"

	"software.sslmate.com/src/go-pkcs12"
)

// parseErrors are profiles that Parse refuses, each a change to one as ikev2setup.sh issues it.
// Each has the start of the error, which names the file and what is wrong with it.
var parseErrors = []struct {
	name   string
	change func(*issued) []byte // The profile; i.zip() where it returns nil.
	want   string
}{
	{"not a zip", func(*issued) []byte { return []byte("PK, but no zip") },
		"profile.zip: zip: not a valid zip file"},
	{"no .p12", func(i *issued) []byte { return zipOf(i.t, file{"client.sswan", i.sswan}) },
		"profile.zip: holds no .p12 file"},
	{"no .sswan", func(i *issued) []byte { return zipOf(i.t, file{"client.p12", i.p12}) },
		"profile.zip: holds no .sswan file"},
	{"two .p12 files", func(i *issued) []byte {
		return zipOf(i.t, file{"client.p12", i.p12}, file{"old/other.p12", i.p12})
	}, "profile.zip: holds two .p12 files, client.p12 and other.p12"},
	{"a file over 1 MiB", func(i *issued) []byte {
		return zipOf(i.t, file{"client.p12", i.p12}, file{"x/client.sswan", make([]byte, 1<<20+1)})
	}, "profile.zip: x/client.sswan: over 1048576 bytes, more than an issued file holds"},
	{"a name no file name is built on", func(i *issued) []byte {
		return zipOf(i.t, file{"my client.p12", i.p12}, file{"my client.sswan", i.sswan})
	}, `profile.zip: my client.p12: "my client" is not a name ikeward builds file names on`},
	{"a .sswan named apart from the .p12", func(i *issued) []byte {
		return zipOf(i.t, file{"a.p12", i.p12}, file{"b.sswan", i.sswan})
	}, "profile.zip: a.p12 and b.sswan are not named alike, as one profile's files are"},
	{"a .mobileconfig named apart from the .p12", func(i *issued) []byte {
		return zipOf(i.t, file{"client.p12", i.p12}, file{"client.sswan", i.sswan},
			file{"client.P12.mobileconfig", i.mobileconfig})
	}, "profile.zip: client.p12 and client.P12.mobileconfig are not named alike"},
	{"a .sswan that is not JSON", func(i *issued) []byte {
		i.sswan = []byte("type = ikev2-cert")
		return nil
	}, "client.sswan: not the JSON of a .sswan: invalid character"},
	{"a .p12 that is not one", func(i *issued) []byte {
		i.p12 = []byte("not a .p12")
		return nil
	}, "client.p12: pkcs12: error reading P12 data"},
	// NSS's pk12util, which ikev2setup.sh runs where OpenSSL is older than 3, writes BER.
	{"a .p12 in BER", func(i *issued) []byte {
		start := 2 + int(i.p12[1]&0x7f) // Past the SEQUENCE's tag and long-form length.
		i.p12 = append(append([]byte{0x30, 0x80}, i.p12[start:]...), 0, 0)
		return nil
	}, "client.p12: pkcs12: error reading P12 data: asn1: syntax error: " +
		"indefinite length found (not DER)"},
	{"a .p12 whose key is not its certificate's", func(i *issued) []byte {
		i.p12 = encodeP12(i.t, pkcs12.LegacyDES, ecKey(i.t), i.pki.cert, nil, "")
		return nil
	}, "client.p12: no certificate in it matches its key"},
	{"a .p12 whose key cannot sign", func(i *issued) []byte {
		key, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			i.t.Fatal(err)
		}
		i.p12 = encodeP12(i.t, pkcs12.LegacyDES, key, i.pki.cert, nil, "")
		return nil
	}, "client.p12: its key, a *ecdh.PrivateKey, cannot sign"},
	{"the CA nowhere, and no .mobileconfig", func(i *issued) []byte {
		other := newCA(i.t, "Other CA", ecKey(i.t))
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{other}, "")
		i.mobileconfig = nil
		return nil
	}, "client.p12: the CA that issued its certificate, CN=Test VPN CA,O=Test VPN, " +
		"is not in it, and the profile holds no .mobileconfig"},
	{"the CA nowhere", func(i *issued) []byte {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, nil, "")
		i.mobileconfig = newMobileconfig(newCA(i.t, "Other CA", ecKey(i.t)).Raw)
		return nil
	}, "client.mobileconfig: no root payload holds the CA that issued client.p12's " +
		"certificate, CN=Test VPN CA,O=Test VPN, which client.p12 lacks too"},
	{"a CA of the same name, with another key, and no .mobileconfig", func(i *issued) []byte {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{caLike(i, ecKey(i.t), nil)}, "")
		i.mobileconfig = nil
		return nil
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but its signature on the " +
		"certificate does not verify: "},
	{"a CA of the same name, with another key, in each file", func(i *issued) []byte {
		other := caLike(i, ecKey(i.t), nil)
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{other}, "")
		i.mobileconfig = newMobileconfig(other.Raw)
		return nil
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but its signature on the " +
		"certificate does not verify: "},
	{"a CA of the same name, with another key, in the .mobileconfig", func(i *issued) []byte {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, nil, "")
		i.mobileconfig = newMobileconfig(caLike(i, ecKey(i.t), nil).Raw)
		return nil
	}, "client.mobileconfig: the CA CN=Test VPN CA,O=Test VPN is there, but its signature on " +
		"the certificate does not verify: "},
	{"the CA, not marked as one", func(i *issued) []byte {
		ca := caLike(i, i.pki.caKey, func(ca *x509.Certificate) { ca.IsCA = false })
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{ca}, "")
		i.mobileconfig = nil
		return nil
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but it is not marked as a CA"},
	{"the CA, without a version 3 certificate's basic constraints", func(i *issued) []byte {
		ca := caLike(i, i.pki.caKey, func(ca *x509.Certificate) { ca.BasicConstraintsValid = false })
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{ca}, "")
		i.mobileconfig = nil
		return nil
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but it is not marked as a CA"},
	{"the CA, whose key may not sign certificates", func(i *issued) []byte {
		ca := caLike(i, i.pki.caKey,
			func(ca *x509.Certificate) { ca.KeyUsage = x509.KeyUsageDigitalSignature })
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{ca}, "")
		i.mobileconfig = nil
		return nil
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but its key usage leaves out " +
		"signing certificates"},
	{"another CA in remote.cert", func(i *issued) []byte {
		i.setRemote(newCA(i.t, "Other CA", ecKey(i.t)))
		return nil
	}, "client.sswan: remote.cert holds the CA CN=Other CA,O=Test VPN, not CN=Test VPN CA," +
		"O=Test VPN, which issued client.p12's certificate, and ikeward takes one CA for both"},
	{"a CA of the same name, with another key, in remote.cert", func(i *issued) []byte {
		i.setRemote(caLike(i, ecKey(i.t), nil))
		return nil
	}, "client.sswan: remote.cert: the CA CN=Test VPN CA,O=Test VPN is there, but its " +
		"signature on the certificate does not verify: "},
	{"the CA nowhere, and a broken .mobileconfig", func(i *issued) []byte {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, nil, "")
		i.mobileconfig = []byte("<plist><dict><key>PayloadContent</key></dict></plist>")
		return nil
	}, `client.mobileconfig: not an XML plist: the key "PayloadContent" has no value`},
}

func TestParseErrors(t *testing.T) {
	for _, test := range parseErrors {
		t.Run(test.name, func(t *testing.T) {
			i := newIssued(t, newPKI(t, ecKey))
			data := test.change(i)
			if data == nil {
				data = i.zip()
			}
			_, err := Parse("profile.zip", data, noPassword(t))
			if err == nil || !strings.HasPrefix(err.Error(), test.want) {
				t.Errorf("Parse: %v, want %s", err, test.want)
			}
		})
	}
}

// TestPasswordErrors checks the errors of a .p12 with a password, none of which holds it.
func TestPasswordErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		password Password
		want     string
	}{
		{"none given", nil, "client.p12: it has a password, and none was given"},
		{"an incorrect one", func() (string, error) { return "the wrong one", nil },
			"client.p12: the password is incorrect"},
		{"an empty one", func() (string, error) { return "", nil },
			"client.p12: the password is incorrect"},
		{"one that UCS-2 cannot hold", func() (string, error) { return "key \U0001F511", nil },
			"client.p12: pkcs12: string contains characters that cannot be encoded in UCS-2"},
		{"none read", func() (string, error) { return "", errors.New("no terminal") },
			"client.p12: cannot read its password: no terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			i := newIssued(t, newPKI(t, ecKey))
			i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{i.pki.ca}, "the right one")
			_, err := Parse("profile.zip", i.zip(), test.password)
			if err == nil || err.Error() != test.want {
				t.Errorf("Parse: %v, want %s", err, test.want)
			}
		})
	}
}
