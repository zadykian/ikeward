package profile

import (
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
)

// findCA sets p's CA to the certificate that issued p's, taken from the first file that holds it.
// The files, in order, are the .p12, whose other certificates are chain, the .sswan's remote.cert,
// remote where set, and the .mobileconfig's root payloads.
func (p *Profile) findCA(chain []*x509.Certificate, remote *x509.Certificate,
	files map[string]member) error {
	p12, sswan := files[".p12"], files[".sswan"]
	// strongSwan's app checks the server by remote.cert; ikeward takes one CA for both ends.
	if remote != nil {
		ca, miss := issuer(p.Certificate, []*x509.Certificate{remote})
		if miss != nil {
			return fmt.Errorf("%s: remote.cert: %w", sswan.name, miss)
		}
		if ca == nil {
			return fmt.Errorf("%s: remote.cert holds the CA %s, not %s, which issued %s's "+
				"certificate, and ikeward takes one CA for both", sswan.name, remote.Subject,
				p.Certificate.Issuer, p12.name)
		}
	}
	ca, miss := issuer(p.Certificate, chain)
	switch {
	case ca != nil:
		p.CA, p.CAFile = ca, p12.name
		return nil
	case remote != nil:
		p.CA, p.CAFile = remote, sswan.name
		return nil
	}
	// A .p12 may hold an older CA of the same name, so a miss there still looks further.
	if miss != nil {
		miss = fmt.Errorf("%s: %w", p12.name, miss)
	}
	mobileconfig, ok := files[".mobileconfig"]
	if !ok {
		return firstError(miss, fmt.Errorf("%s: the CA that issued its certificate, %s, "+
			"is not in it, and the profile holds no .mobileconfig", p12.name, p.Certificate.Issuer))
	}
	roots, err := rootPayloads(mobileconfig)
	if err != nil {
		return err
	}
	ca, rootMiss := issuer(p.Certificate, roots)
	if ca == nil {
		if rootMiss != nil {
			rootMiss = fmt.Errorf("%s: %w", mobileconfig.name, rootMiss)
		}
		return firstError(miss, rootMiss, fmt.Errorf("%s: no root payload holds the CA "+
			"that issued %s's certificate, %s, which %s lacks too", mobileconfig.name, p12.name,
			p.Certificate.Issuer, p12.name))
	}
	p.CA, p.CAFile = ca, mobileconfig.name
	return nil
}

// firstError returns the first of errs that is not nil.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// issuer returns the certificate of cas that issued cert. Where none did, it returns nil and,
// where one bears the name of cert's issuer, why that one did not.
func issuer(cert *x509.Certificate, cas []*x509.Certificate) (*x509.Certificate, error) {
	var miss error
	for _, ca := range cas {
		// strongSwan, too, looks an issuer up by its subject, then checks it.
		if !bytes.Equal(cert.RawIssuer, ca.RawSubject) {
			continue
		}
		err := checkIssuer(cert, ca)
		if err == nil {
			return ca, nil
		}
		if miss == nil {
			miss = fmt.Errorf("the CA %s is there, but %w", ca.Subject, err)
		}
	}
	return nil, miss
}

// checkIssuer is why ca cannot have issued cert, or nil. It checks what x509's CheckSignatureFrom
// does, but takes a SHA-1 signature, which CheckSignatureFrom refuses and strongSwan accepts.
func checkIssuer(cert, ca *x509.Certificate) error {
	if !isCA(ca) {
		return errors.New("it is not marked as a CA")
	}
	if ca.KeyUsage != 0 && ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		return errors.New("its key usage leaves out signing certificates")
	}
	err := ca.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature)
	if err != nil {
		return fmt.Errorf("its signature on the certificate does not verify: %w", err)
	}
	return nil
}

// isCA is whether c's basic constraints let it issue certificates. A version 1 certificate has
// none, and may.
func isCA(c *x509.Certificate) bool {
	if c.BasicConstraintsValid {
		return c.IsCA
	}
	return c.Version < 3
}
