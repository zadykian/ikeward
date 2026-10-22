package files

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/zadykian/ikeward/internal/profile"
)

// The directories of swanctl's credentials, from which swanctl --load-all loads them.
const (
	certDir = "/etc/swanctl/x509/"
	caDir   = "/etc/swanctl/x509ca/"
	keyDir  = "/etc/swanctl/private/"
)

// Credentials returns the profile's credentials as files, under the names the connection gives
// them. They are the client's certificate, NAME-cert.pem, the CA, and the client's key,
// NAME-key.pem, which only root may read. The key is PKCS #8 in PEM, which strongSwan reads for
// any kind of key.
func Credentials(p *profile.Profile) ([]File, error) {
	if p == nil || p.Key == nil || p.Certificate == nil || p.CA == nil {
		return nil, errors.New("cannot write the credentials: the profile lacks them")
	}
	key, err := x509.MarshalPKCS8PrivateKey(p.Key)
	if err != nil {
		return nil, fmt.Errorf("cannot write the key of %s.p12: %w", p.Name, err)
	}
	return []File{
		{Path: certDir + p.Name + "-cert.pem", Mode: 0o644, Data: certificate(p.Certificate)},
		{Path: caDir + CACert, Mode: 0o644, Data: certificate(p.CA)},
		{Path: keyDir + p.Name + "-key.pem", Mode: 0o600,
			Data: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})},
	}, nil
}

// certificate is c in PEM.
func certificate(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}
