package profile

import (
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"

	"software.sslmate.com/src/go-pkcs12"
)

// readP12 sets p's key and certificate from the .p12 f, asking password for its password where it
// has one, and returns the other certificates it holds. The password goes into no error.
func (p *Profile) readP12(f File, password Password) ([]*x509.Certificate, error) {
	// An empty password also opens a .p12 that has none at all.
	key, cert, chain, err := pkcs12.DecodeChain(f.Data, "")
	if errors.Is(err, pkcs12.ErrIncorrectPassword) {
		if password == nil {
			return nil, fmt.Errorf("%s: it has a password, and none was given", f.Name)
		}
		secret, readErr := password()
		if readErr != nil {
			return nil, fmt.Errorf("%s: cannot read its password: %w", f.Name, readErr)
		}
		p.Password = true
		key, cert, chain, err = pkcs12.DecodeChain(f.Data, secret)
	}
	if errors.Is(err, pkcs12.ErrIncorrectPassword) {
		return nil, fmt.Errorf("%s: the password is incorrect", f.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("%s: its key, a %T, cannot sign", f.Name, key)
	}
	// DecodeChain takes the first certificate for the client's, which not every tool puts first.
	certs := append([]*x509.Certificate{cert}, chain...)
	i := slices.IndexFunc(certs, func(c *x509.Certificate) bool { return matches(signer, c) })
	if i < 0 {
		return nil, fmt.Errorf("%s: no certificate in it matches its key", f.Name)
	}
	p.Key, p.Certificate = signer, certs[i]
	return slices.Delete(certs, i, i+1), nil
}

// matches is whether cert is key's certificate.
func matches(key crypto.Signer, cert *x509.Certificate) bool {
	public, ok := key.Public().(interface{ Equal(x crypto.PublicKey) bool })
	return ok && public.Equal(cert.PublicKey)
}
