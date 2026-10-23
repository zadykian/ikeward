package host

import (
	"bytes"
	"errors"
	"iter"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/strongswan/govici/vici"

	"github.com/zadykian/ikeward/internal/status"
)

// capturedSA is a list-sa event of a live server's IKE SA, as charon sent it. The connection's name
// and the server's address differ, and the keys IKESAs does not read are gone.
func capturedSA(t *testing.T) *vici.Message {
	t.Helper()
	return message(t, "ikeward", message(t,
		"uniqueid", "60", "version", "2", "state", "ESTABLISHED", "remote-host", "198.51.100.10",
		"local-vips", []string{"203.0.113.130"},
		"child-sas", message(t,
			"ikeward-62", message(t, "name", "ikeward", "uniqueid", "62", "state", "REKEYED",
				"local-ts", []string{"203.0.113.130/32"}, "remote-ts", []string{"10.88.0.0/16"}),
			"ikeward-61", message(t, "name", "ikeward", "uniqueid", "61", "state", "INSTALLED",
				"encr-alg", "AES_GCM_16", "local-ts", []string{"203.0.113.130/32"},
				"remote-ts", []string{"10.88.0.0/16"}))))
}

// IKESAs asks charon to list the connection's IKE SAs alone, and reads each, its CHILD SAs in
// charon's order.
func TestVICI(t *testing.T) {
	connecting := message(t, "ikeward", message(t, "state", "CONNECTING"))
	socket, requests := fakeCharon(t, []*vici.Message{capturedSA(t), connecting}, vici.NewMessage())
	got, err := VICI{Socket: socket}.IKESAs(t.Context(), "ikeward")
	if err != nil {
		t.Fatal(err)
	}
	want := []status.IKESA{{State: "ESTABLISHED", Remote: "198.51.100.10",
		VIPs: []string{"203.0.113.130"},
		Children: []status.ChildSA{
			{Name: "ikeward", State: "REKEYED", LocalTS: []string{"203.0.113.130/32"},
				RemoteTS: []string{"10.88.0.0/16"}},
			{Name: "ikeward", State: "INSTALLED", LocalTS: []string{"203.0.113.130/32"},
				RemoteTS: []string{"10.88.0.0/16"}}}},
		{State: "CONNECTING"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IKESAs returned\n%+v\nwant\n%+v", got, want)
	}
	request := packet(cmdRequest, "list-sas", encode(message(t, "ike", "ikeward")))[4:]
	if got := <-requests; !bytes.Equal(got, request) {
		t.Errorf("sent %q, want %q", got, request)
	}
}

// With NoBlock, IKESAs asks charon not to wait for the IKE SAs it holds, as for updown.
func TestVICINoBlock(t *testing.T) {
	socket, requests := fakeCharon(t, []*vici.Message{capturedSA(t)}, vici.NewMessage())
	got, err := VICI{Socket: socket, NoBlock: true}.IKESAs(t.Context(), "ikeward")
	if err != nil || len(got) != 1 || got[0].State != "ESTABLISHED" {
		t.Fatalf("%+v, %v", got, err)
	}
	request := packet(cmdRequest, "list-sas",
		encode(message(t, "ike", "ikeward", "noblock", "yes")))[4:]
	if got := <-requests; !bytes.Equal(got, request) {
		t.Errorf("sent %q, want %q", got, request)
	}
}

// Where charon fails the command, or does not listen, IKESAs says so.
func TestVICIErrors(t *testing.T) {
	socket, _ := fakeCharon(t, nil, message(t, "success", "no", "errmsg", "no such IKE SA"))
	_, err := VICI{Socket: socket}.IKESAs(t.Context(), "ikeward")
	if want := "list-sas: vici: command failed: no such IKE SA"; err == nil || err.Error() != want {
		t.Errorf("%v, want %s", err, want)
	}

	missing := filepath.Join(t.TempDir(), "charon.vici")
	_, err = VICI{Socket: missing}.IKESAs(t.Context(), "ikeward")
	if want := missing + ": no such file or directory"; err == nil || err.Error() != want {
		t.Errorf("%v, want %s", err, want)
	}
}

// events yields messages, then err, unless err is nil.
func events(messages []*vici.Message, err error) iter.Seq2[*vici.Message, error] {
	return func(yield func(*vici.Message, error) bool) {
		for _, m := range messages {
			if !yield(m, nil) {
				return
			}
		}
		if err != nil {
			yield(nil, err)
		}
	}
}

// ikeSAs refuses events it cannot read, and returns no IKE SA where there is none.
func TestIKESAs(t *testing.T) {
	for _, test := range []struct {
		name   string
		events []*vici.Message
		err    error
		want   string
	}{
		{"broken", []*vici.Message{capturedSA(t)}, errors.New("connection reset"),
			"list-sas: connection reset"},
		{"no section", []*vici.Message{message(t, "ikeward", "up")}, nil,
			"list-sas: ikeward is no section"},
		{"state a list", []*vici.Message{message(t, "ikeward",
			message(t, "state", []string{"up"}))}, nil,
			"list-sas: ikeward: vici: error unmarshaling message: incompatible types: " +
				"string and []string"},
		{"child no section", []*vici.Message{message(t, "ikeward",
			message(t, "child-sas", message(t, "ikeward-1", "up")))}, nil,
			"list-sas: ikeward-1 is no section"},
		{"child state a list", []*vici.Message{message(t, "ikeward", message(t, "child-sas",
			message(t, "ikeward-1", message(t, "state", []string{"up"}))))}, nil,
			"list-sas: ikeward-1: vici: error unmarshaling message: incompatible types: " +
				"string and []string"},
	} {
		sas, err := ikeSAs(events(test.events, test.err))
		if err == nil || err.Error() != test.want || sas != nil {
			t.Errorf("%s: %v, %v; want %s", test.name, sas, err, test.want)
		}
	}
	sas, err := ikeSAs(events(nil, nil))
	if sas != nil || err != nil {
		t.Errorf("no events: %v, %v", sas, err)
	}
}
