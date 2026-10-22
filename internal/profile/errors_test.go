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
	change func(*issued)
	want   string
}{
	{"a file over 1 MiB", func(i *issued) { i.sswan.Data = make([]byte, 1<<20+1) },
		"client.sswan: over 1048576 bytes, more than an issued file holds"},
	{"a name no file name is built on", func(i *issued) {
		i.p12.Name, i.sswan.Name, i.mobileconfig.Name = "my client.p12", "my client.sswan", ""
	}, `my client.p12: "my client" is not a name ikeward builds file names on`},
	{"a .sswan named apart from the .p12", func(i *issued) {
		i.p12.Name, i.sswan.Name, i.mobileconfig.Name = "a.p12", "b.sswan", ""
	}, "a.p12 and b.sswan are not named alike, as one profile's files are"},
	{"a .mobileconfig named apart from the .p12", func(i *issued) {
		i.mobileconfig.Name = "client.P12.mobileconfig"
	}, "client.p12 and client.P12.mobileconfig are not named alike"},
	{"a .sswan that is not JSON", func(i *issued) {
		i.sswan.Data = []byte("type = ikev2-cert")
	}, "client.sswan: not the JSON of a .sswan: invalid character"},
	{"a .p12 that is not one", func(i *issued) {
		i.p12.Data = []byte("not a .p12")
	}, "client.p12: pkcs12: error reading P12 data"},
	// NSS's pk12util, which ikev2setup.sh runs where OpenSSL is older than 3, writes BER.
	{"a .p12 in BER", func(i *issued) {
		start := 2 + int(i.p12.Data[1]&0x7f) // Past the SEQUENCE's tag and long-form length.
		i.p12.Data = append(append([]byte{0x30, 0x80}, i.p12.Data[start:]...), 0, 0)
	}, "client.p12: pkcs12: error reading P12 data: asn1: syntax error: " +
		"indefinite length found (not DER)"},
	{"a .p12 whose key is not its certificate's", func(i *issued) {
		i.p12.Data = encodeP12(i.t, pkcs12.LegacyDES, ecKey(i.t), i.pki.cert, nil, "")
	}, "client.p12: no certificate in it matches its key"},
	{"a .p12 whose key cannot sign", func(i *issued) {
		key, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			i.t.Fatal(err)
		}
		i.p12.Data = encodeP12(i.t, pkcs12.LegacyDES, key, i.pki.cert, nil, "")
	}, "client.p12: its key, a *ecdh.PrivateKey, cannot sign"},
	{"the CA nowhere, and no .mobileconfig", func(i *issued) {
		other := newCA(i.t, "Other CA", ecKey(i.t))
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{other}, "")
		i.mobileconfig = File{}
	}, "client.p12: the CA that issued its certificate, CN=Test VPN CA,O=Test VPN, " +
		"is not in it, and no .mobileconfig was given"},
	{"the CA nowhere", func(i *issued) {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, nil, "")
		i.mobileconfig.Data = newMobileconfig(newCA(i.t, "Other CA", ecKey(i.t)).Raw)
	}, "client.mobileconfig: no root payload holds the CA that issued client.p12's " +
		"certificate, CN=Test VPN CA,O=Test VPN, which client.p12 lacks too"},
	{"a CA of the same name, with another key, and no .mobileconfig", func(i *issued) {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{caLike(i, ecKey(i.t), nil)}, "")
		i.mobileconfig = File{}
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but its signature on the " +
		"certificate does not verify: "},
	{"a CA of the same name, with another key, in each file", func(i *issued) {
		other := caLike(i, ecKey(i.t), nil)
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{other}, "")
		i.mobileconfig.Data = newMobileconfig(other.Raw)
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but its signature on the " +
		"certificate does not verify: "},
	{"a CA of the same name, with another key, in the .mobileconfig", func(i *issued) {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, nil, "")
		i.mobileconfig.Data = newMobileconfig(caLike(i, ecKey(i.t), nil).Raw)
	}, "client.mobileconfig: the CA CN=Test VPN CA,O=Test VPN is there, but its signature on " +
		"the certificate does not verify: "},
	{"the CA, not marked as one", func(i *issued) {
		ca := caLike(i, i.pki.caKey, func(ca *x509.Certificate) { ca.IsCA = false })
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{ca}, "")
		i.mobileconfig = File{}
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but it is not marked as a CA"},
	{"the CA, without a version 3 certificate's basic constraints", func(i *issued) {
		ca := caLike(i, i.pki.caKey, func(ca *x509.Certificate) { ca.BasicConstraintsValid = false })
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{ca}, "")
		i.mobileconfig = File{}
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but it is not marked as a CA"},
	{"the CA, whose key may not sign certificates", func(i *issued) {
		ca := caLike(i, i.pki.caKey,
			func(ca *x509.Certificate) { ca.KeyUsage = x509.KeyUsageDigitalSignature })
		i.setP12(pkcs12.LegacyDES, i.pki.cert, []*x509.Certificate{ca}, "")
		i.mobileconfig = File{}
	}, "client.p12: the CA CN=Test VPN CA,O=Test VPN is there, but its key usage leaves out " +
		"signing certificates"},
	{"another CA in remote.cert", func(i *issued) {
		i.setRemote(newCA(i.t, "Other CA", ecKey(i.t)))
	}, "client.sswan: remote.cert holds the CA CN=Other CA,O=Test VPN, not CN=Test VPN CA," +
		"O=Test VPN, which issued client.p12's certificate, and ikeward takes one CA for both"},
	{"a CA of the same name, with another key, in remote.cert", func(i *issued) {
		i.setRemote(caLike(i, ecKey(i.t), nil))
	}, "client.sswan: remote.cert: the CA CN=Test VPN CA,O=Test VPN is there, but its " +
		"signature on the certificate does not verify: "},
	{"the CA nowhere, and a broken .mobileconfig", func(i *issued) {
		i.setP12(pkcs12.LegacyDES, i.pki.cert, nil, "")
		i.mobileconfig.Data = []byte("<plist><dict><key>PayloadContent</key></dict></plist>")
	}, `client.mobileconfig: not an XML plist: the key "PayloadContent" has no value`},
}

func TestParseErrors(t *testing.T) {
	for _, test := range parseErrors {
		t.Run(test.name, func(t *testing.T) {
			i := newIssued(t, newPKI(t, ecKey))
			test.change(i)
			_, err := Parse(i.files(), noPassword(t))
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
			_, err := Parse(i.files(), test.password)
			if err == nil || err.Error() != test.want {
				t.Errorf("Parse: %v, want %s", err, test.want)
			}
		})
	}
}
