package host

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/strongswan/govici/vici"

	"github.com/zadykian/ikeward/internal/install"
)

// HasConn asks charon to list the connection alone, and finds it where charon lists it.
func TestVICIHasConn(t *testing.T) {
	for _, test := range []struct {
		name   string
		events []*vici.Message
		want   bool
	}{
		{"loaded", []*vici.Message{message(t, "ikeward",
			message(t, "local_addrs", []string{"%any"}, "version", "IKEv2"))}, true},
		{"another", []*vici.Message{message(t, "other", message(t, "version", "IKEv2"))}, false},
		{"none", nil, false},
	} {
		socket, requests := fakeCharon(t, test.events, vici.NewMessage())
		var charon install.Charon = VICI{Socket: socket}
		got, err := charon.HasConn(t.Context(), "ikeward")
		if err != nil || got != test.want {
			t.Errorf("%s: %v, %v; want %v", test.name, got, err, test.want)
		}
		request := packet(cmdRequest, "list-conns", encode(message(t, "ike", "ikeward")))[4:]
		if got := <-requests; !bytes.Equal(got, request) {
			t.Errorf("%s: sent %q, want %q", test.name, got, request)
		}
	}
}

// counting is n bytes, counting up from first and on from 0x00 past 0xff.
func counting(first byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = first + byte(i)
	}
	return b
}

// Certs asks charon to list the X.509 certificates, and reads each, its bytes as charon sent them.
func TestVICICerts(t *testing.T) {
	client, ca, ocsp := counting(0x30, 600), counting(0xff, 300), counting(0x80, 2)
	socket, requests := fakeCharon(t, []*vici.Message{
		message(t, "type", "X509", "flag", "NONE", "has_privkey", "yes", "data", string(client)),
		message(t, "type", "X509", "flag", "CA", "data", string(ca)),
		message(t, "type", "X509", "flag", "OCSP", "data", string(ocsp))}, vici.NewMessage())
	var charon install.Charon = VICI{Socket: socket}
	got, err := charon.Certs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []install.Cert{{DER: client, Key: true}, {DER: ca, CA: true}, {DER: ocsp}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Certs returned\n%+v\nwant\n%+v", got, want)
	}
	request := packet(cmdRequest, "list-certs", encode(message(t, "type", "X509")))[4:]
	if got := <-requests; !bytes.Equal(got, request) {
		t.Errorf("sent %q, want %q", got, request)
	}

	socket, _ = fakeCharon(t, nil, vici.NewMessage())
	if got, err := (VICI{Socket: socket}).Certs(t.Context()); got != nil || err != nil {
		t.Errorf("no certificates: %v, %v", got, err)
	}
}

// Where charon fails the command, after a connection too, HasConn says so.
func TestVICIHasConnErrors(t *testing.T) {
	loaded := message(t, "ikeward", message(t, "version", "IKEv2"))
	socket, _ := fakeCharon(t, []*vici.Message{loaded}, message(t, "success", "no", "errmsg", "busy"))
	got, err := VICI{Socket: socket}.HasConn(t.Context(), "ikeward")
	if want := "list-conns: vici: command failed: busy"; got || err == nil || err.Error() != want {
		t.Errorf("%v, %v; want %s", got, err, want)
	}

	missing := filepath.Join(t.TempDir(), "charon.vici")
	_, err = VICI{Socket: missing}.HasConn(t.Context(), "ikeward")
	if want := missing + ": no such file or directory"; err == nil || err.Error() != want {
		t.Errorf("%v, want %s", err, want)
	}
}

// Where charon fails the command, or sends a certificate Certs cannot read, Certs says so.
func TestVICICertsErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		events   []*vici.Message
		response *vici.Message
		want     string
	}{
		{"failed", []*vici.Message{message(t, "flag", "CA", "data", "\x30\x00")},
			message(t, "success", "no", "errmsg", "busy"), "list-certs: vici: command failed: busy"},
		{"flag a list", []*vici.Message{message(t, "flag", []string{"CA"})}, vici.NewMessage(),
			"list-certs: vici: error unmarshaling message: incompatible types: " +
				"string and []string"},
		{"has_privkey neither yes nor no", []*vici.Message{message(t, "has_privkey", "maybe")},
			vici.NewMessage(),
			"list-certs: vici: error unmarshaling message: failed to parse value: maybe as bool"},
	} {
		socket, _ := fakeCharon(t, test.events, test.response)
		got, err := VICI{Socket: socket}.Certs(t.Context())
		if err == nil || err.Error() != test.want || got != nil {
			t.Errorf("%s: %v, %v; want %s", test.name, got, err, test.want)
		}
	}

	missing := filepath.Join(t.TempDir(), "charon.vici")
	_, err := VICI{Socket: missing}.Certs(t.Context())
	if want := missing + ": no such file or directory"; err == nil || err.Error() != want {
		t.Errorf("%v, want %s", err, want)
	}
}
