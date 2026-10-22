package profile

import (
	"encoding/base64"
	"encoding/pem"
	"slices"
	"strings"
	"testing"
)

// remote sets the .sswan's remote.KEY to value.
func remote(key string, value any) func(map[string]any) {
	return func(object map[string]any) {
		object["remote"].(map[string]any)[key] = value
	}
}

func TestReadSSwan(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any)
		want   []any // Server, Port, RemoteID and LocalID.
	}{
		{"as ikev2setup.sh writes it", nil,
			[]any{"vpn.example.org", 500, "vpn.example.org", ""}},
		{"with identities", func(object map[string]any) {
			remote("id", "CN=vpn.example.org, O=Test VPN")(object)
			object["local"].(map[string]any)["id"] = "client@example.org"
		}, []any{"vpn.example.org", 500, "CN=vpn.example.org, O=Test VPN", "client@example.org"}},
		{"with a port", remote("port", 4500),
			[]any{"vpn.example.org", 4500, "vpn.example.org", ""}},
		{"with a port in a string", remote("port", "4500"),
			[]any{"vpn.example.org", 4500, "vpn.example.org", ""}},
		{"with an IPv4 address", remote("addr", "192.0.2.1"),
			[]any{"192.0.2.1", 500, "192.0.2.1", ""}},
		{"with an IPv6 address", remote("addr", "2001:db8::1"),
			[]any{"2001:db8::1", 500, "2001:db8::1", ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var p Profile
			ca, err := p.readSSwan(File{"client.sswan", newSSwan(t, nil, test.change)})
			if err != nil || ca != nil {
				t.Fatalf("readSSwan: %v, the CA %v, want neither", err, ca)
			}
			got := []any{p.Server, p.Port, p.RemoteID, p.LocalID}
			if !slices.Equal(got, test.want) {
				t.Errorf("readSSwan read %q, want %q", got, test.want)
			}
			if p.IKEProposals != "aes256-sha256-modp2048" || p.ESPProposals != "aes128gcm16" {
				t.Errorf("readSSwan read the proposals %q and %q", p.IKEProposals, p.ESPProposals)
			}
		})
	}
}

func TestReadSSwanErrors(t *testing.T) {
	for _, test := range []struct {
		change func(map[string]any)
		want   string
	}{
		{func(object map[string]any) { object["type"] = "ikev2-eap" },
			`the type is "ikev2-eap", and ikeward sets up only ikev2-cert, a login by certificate`},
		{func(object map[string]any) { delete(object, "type") },
			`the type is "", and ikeward sets up only ikev2-cert, a login by certificate`},
		{func(object map[string]any) { delete(object, "remote") },
			`remote.addr "" is neither a host name nor an address`},
		{remote("addr", "vpn example.org"),
			`remote.addr "vpn example.org" is neither a host name nor an address`},
		{remote("addr", "vpn.example.org\n}"),
			`remote.addr "vpn.example.org\n}" is neither a host name nor an address`},
		{remote("addr", "-vpn.example.org"),
			`remote.addr "-vpn.example.org" is neither a host name nor an address`},
		{remote("addr", "fe80::1%eth0"),
			`remote.addr "fe80::1%eth0" is neither a host name nor an address`},
		{remote("addr", 1), "not the JSON of a .sswan: json: cannot unmarshal number"},
		{remote("port", 0), "remote.port 0 is not a port"},
		{remote("port", 65536), "remote.port 65536 is not a port"},
		{remote("port", "five"), `remote.port "five" is not a port`},
		{remote("port", 4500.5), "remote.port 4500.5 is not a port"},
		{remote("id", `vpn"}`), `remote.id "vpn\"}" holds a character an identity cannot`},
		{func(object map[string]any) { object["local"].(map[string]any)["id"] = "a\nb" },
			`local.id "a\nb" holds a character an identity cannot`},
		{func(object map[string]any) { delete(object, "ike-proposal") },
			`ike-proposal "" is not in strongSwan's syntax for proposals`},
		{func(object map[string]any) { object["esp-proposal"] = "aes128gcm16 }" },
			`esp-proposal "aes128gcm16 }" is not in strongSwan's syntax for proposals`},
		{remote("cert", "not Base64"), "remote.cert is not Base64: illegal base64 data"},
		{remote("cert", base64.StdEncoding.EncodeToString([]byte("not a certificate"))),
			"remote.cert: x509: malformed certificate"},
		{func(object map[string]any) { object["esp-proposal"] = "aes128gcm16,,aes256gcm16" },
			`esp-proposal "aes128gcm16,,aes256gcm16" is not in strongSwan's syntax for proposals`},
	} {
		var p Profile
		_, err := p.readSSwan(File{"client.sswan", newSSwan(t, nil, test.change)})
		if want := "client.sswan: " + test.want; err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("readSSwan: %v, want %s", err, want)
		}
	}
}

// TestReadSSwanProposals checks proposals as strongSwan's documentation gives them.
func TestReadSSwanProposals(t *testing.T) {
	for _, proposal := range []string{
		"aes256-sha256-modp2048",
		"aes256gcm16-prfsha256-ecp256",
		"aes128-sha256-x25519,aes256gcm16-prfsha384-ke1_mlkem768",
		"chacha20poly1305",
	} {
		change := func(object map[string]any) { object["ike-proposal"] = proposal }
		var p Profile
		if _, err := p.readSSwan(File{"client.sswan", newSSwan(t, nil, change)}); err != nil {
			t.Errorf("%s: %v", proposal, err)
		}
	}
}

// TestReadSSwanCert checks remote.cert, which strongSwan's app takes for the server's CA or, where
// the certificate is no CA, for the server's own certificate.
func TestReadSSwanCert(t *testing.T) {
	pki := newPKI(t, ecKey)
	pemCA := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pki.ca.Raw})
	for _, data := range [][]byte{pki.ca.Raw, pemCA} {
		var p Profile
		ca, err := p.readSSwan(File{"client.sswan",
			newSSwan(t, nil, remote("cert", base64.StdEncoding.EncodeToString(data)))})
		if err != nil || ca == nil || !ca.Equal(pki.ca) {
			t.Errorf("readSSwan: %v, the CA %v, want %s", err, ca, pki.ca.Subject)
		}
	}

	var p Profile
	_, err := p.readSSwan(File{"client.sswan",
		newSSwan(t, nil, remote("cert", base64.StdEncoding.EncodeToString(pki.cert.Raw)))})
	want := "client.sswan: remote.cert pins the server's certificate, CN=client,O=Test VPN, " +
		"and ikeward checks the server only by its CA"
	if err == nil || err.Error() != want {
		t.Errorf("readSSwan: %v, want %s", err, want)
	}
}
