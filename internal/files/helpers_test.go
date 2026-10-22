package files

import (
	"net/netip"

	"github.com/zadykian/ikeward/internal/profile"
)

// testSettings are the settings of testdata's files, as install's flags give them.
var testSettings = Settings{
	DNS: netip.MustParseAddr("192.0.2.10"), Domains: []string{"example.test"},
	Probe: "example.test", Floors: []netip.Prefix{netip.MustParsePrefix("10.88.0.0/16")},
}

// profiles are the profiles whose files testdata/NAME holds, each under the path it goes to.
var profiles = map[string]*profile.Profile{
	// As ikev2setup.sh issues a profile, and as a live server's has it.
	"issued": {
		Name: "client", Server: "vpn.example.org", Port: 500, RemoteID: "vpn.example.org",
		IKEProposals: "aes256-sha256-modp2048", ESPProposals: "aes128gcm16",
	},
	// Every value the .sswan may set otherwise: identities that swanctl.conf needs quoted, a
	// server's address, another port, and lists of proposals.
	"options": {
		Name: "a_client.2", Server: "2001:db8::1", Port: 4500,
		RemoteID: "O=Test VPN, CN=vpn.example.org", LocalID: "O=Test VPN, CN=client",
		IKEProposals: "aes256gcm16-prfsha384-ecp384,aes256-sha256-modp2048",
		ESPProposals: "aes256gcm16-ecp384,aes128gcm16",
	},
}
