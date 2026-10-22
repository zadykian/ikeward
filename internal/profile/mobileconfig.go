package profile

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

// rootPayloads returns the certificates of the .mobileconfig f's root payloads, the
// com.apple.security.root dicts in its PayloadContent.
func rootPayloads(f member) ([]*x509.Certificate, error) {
	// A signed profile is DER, which starts with a SEQUENCE, around the plist.
	if bytes.HasPrefix(f.data, []byte{0x30}) {
		return nil, fmt.Errorf("%s: it is signed, and ikeward reads only an unsigned XML plist",
			f.name)
	}
	top, err := decodePlist(f.data)
	if err != nil {
		return nil, fmt.Errorf("%s: not an XML plist: %w", f.name, err)
	}
	dict, _ := top.(map[string]any)
	payloads, _ := dict["PayloadContent"].([]any)
	var certs []*x509.Certificate
	for i, payload := range payloads {
		payload, _ := payload.(map[string]any)
		if payload["PayloadType"] != "com.apple.security.root" {
			continue
		}
		data, ok := payload["PayloadContent"].([]byte)
		if !ok {
			return nil, fmt.Errorf("%s: payload %d, a root, holds no data", f.name, i+1)
		}
		cert, err := parseCertificate(data)
		if err != nil {
			return nil, fmt.Errorf("%s: payload %d, a root: %w", f.name, i+1, err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("%s: it holds no root payload, com.apple.security.root", f.name)
	}
	return certs, nil
}

// parseCertificate parses DER, which Apple's profiles hold, or PEM, which some generators put.
func parseCertificate(data []byte) (*x509.Certificate, error) {
	if block, _ := pem.Decode(data); block != nil {
		data = block.Bytes
	}
	return x509.ParseCertificate(data)
}

// decodePlist decodes an XML property list: a dict as map[string]any, an array as []any, and data
// as []byte. It decodes true and false as bool, and a string, integer, real or date as its text.
func decodePlist(data []byte) (any, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	start, err := child(d)
	if err == nil && (start == nil || start.Name.Local != "plist") {
		err = errors.New("no <plist> element")
	}
	if err == nil {
		start, err = child(d)
	}
	if err == nil && start == nil {
		err = errors.New("an empty <plist>")
	}
	if err != nil {
		return nil, err
	}
	return plistValue(d, *start)
}

// child returns the start of the next element in the one d is in, or nil at that one's end.
func child(d *xml.Decoder) (*xml.StartElement, error) {
	for {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			return &token, nil
		case xml.EndElement:
			return nil, nil
		}
	}
}

// plistValue decodes the value whose element start opens.
func plistValue(d *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "dict":
		return plistDict(d)
	case "array":
		var array []any
		for {
			next, err := child(d)
			if err != nil || next == nil {
				return array, err
			}
			value, err := plistValue(d, *next)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
	case "true", "false":
		return start.Name.Local == "true", d.Skip()
	case "data":
		var text string
		if err := d.DecodeElement(&text, &start); err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	case "string", "integer", "real", "date":
		var text string
		err := d.DecodeElement(&text, &start)
		return text, err
	}
	return nil, fmt.Errorf("<%s>, which a plist does not have", start.Name.Local)
}

// plistDict decodes a dict's keys and values, up to its end.
func plistDict(d *xml.Decoder) (map[string]any, error) {
	dict := map[string]any{}
	for {
		start, err := child(d)
		if err != nil || start == nil {
			return dict, err
		}
		var key string
		if start.Name.Local != "key" {
			return nil, fmt.Errorf("<%s> where a dict has a <key>", start.Name.Local)
		}
		if err := d.DecodeElement(&key, start); err != nil {
			return nil, err
		}
		if start, err = child(d); err == nil && start == nil {
			err = fmt.Errorf("the key %q has no value", key)
		}
		if err != nil {
			return nil, err
		}
		if dict[key], err = plistValue(d, *start); err != nil {
			return nil, err
		}
	}
}
