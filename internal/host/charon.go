package host

import (
	"context"
	"fmt"
	"iter"
	"slices"

	"github.com/strongswan/govici/vici"

	"github.com/zadykian/ikeward/internal/install"
)

// HasConn is whether charon has loaded the connection named name.
func (v VICI) HasConn(ctx context.Context, name string) (bool, error) {
	in := vici.NewMessage()
	if err := in.Set("ike", name); err != nil {
		return false, err
	}
	read := func(events iter.Seq2[*vici.Message, error]) (bool, error) {
		return hasConn(events, name)
	}
	return stream(ctx, v.Socket, "list-conns", "list-conn", in, read)
}

// Certs returns the X.509 certificates charon holds.
func (v VICI) Certs(ctx context.Context) ([]install.Cert, error) {
	in := vici.NewMessage()
	if err := in.Set("type", "X509"); err != nil {
		return nil, err
	}
	return stream(ctx, v.Socket, "list-certs", "list-cert", in, certs)
}

// hasConn is whether list-conns' events, which hold a connection each, keyed by its name, hold
// the one named name.
func hasConn(events iter.Seq2[*vici.Message, error], name string) (bool, error) {
	found := false
	for event, err := range events {
		if err != nil {
			return false, fmt.Errorf("list-conns: %w", err)
		}
		found = found || slices.Contains(event.Keys(), name)
	}
	return found, nil
}

// viciCert is what certs reads of a certificate in a list-cert event. Flag is NONE, CA, AA or
// OCSP, and Key is set where charon holds the private key.
type viciCert struct {
	Flag string `vici:"flag"`
	Key  bool   `vici:"has_privkey"`
	// DER holds the certificate's bytes as they are, as each value of VICI's does.
	DER string `vici:"data"`
}

// certs reads the certificates of list-certs' events, which hold one each.
func certs(events iter.Seq2[*vici.Message, error]) ([]install.Cert, error) {
	var held []install.Cert
	for event, err := range events {
		if err != nil {
			return nil, fmt.Errorf("list-certs: %w", err)
		}
		var c viciCert
		if err := vici.UnmarshalMessage(event, &c); err != nil {
			return nil, fmt.Errorf("list-certs: %w", err)
		}
		held = append(held, install.Cert{DER: []byte(c.DER), CA: c.Flag == "CA", Key: c.Key})
	}
	return held, nil
}
