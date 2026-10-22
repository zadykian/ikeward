package host

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"iter"
	"net"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/strongswan/govici/vici"

	"github.com/zadykian/ikeward/internal/status"
)

// VICI's packet types and message elements, as strongSwan's src/libcharon/plugins/vici/README.md
// gives them.
const (
	cmdRequest      = 0
	cmdResponse     = 1
	eventRegister   = 3
	eventUnregister = 4
	eventConfirm    = 5
	event           = 7

	sectionStart = 1
	sectionEnd   = 2
	keyValue     = 3
	listStart    = 4
	listItem     = 5
	listEnd      = 6
)

// encode is m as VICI encodes a message.
func encode(m *vici.Message) []byte {
	var b bytes.Buffer
	name := func(kind byte, s string) {
		b.WriteByte(kind)
		b.WriteByte(byte(len(s)))
		b.WriteString(s)
	}
	value := func(s string) {
		b.Write(binary.BigEndian.AppendUint16(nil, uint16(len(s))))
		b.WriteString(s)
	}
	for _, key := range m.Keys() {
		switch v := m.Get(key).(type) {
		case string:
			name(keyValue, key)
			value(v)
		case []string:
			name(listStart, key)
			for _, item := range v {
				b.WriteByte(listItem)
				value(item)
			}
			b.WriteByte(listEnd)
		case *vici.Message:
			name(sectionStart, key)
			b.Write(encode(v))
			b.WriteByte(sectionEnd)
		}
	}
	return b.Bytes()
}

// packet is a VICI packet of type kind, holding body, and named name where kind takes a name.
func packet(kind byte, name string, body []byte) []byte {
	p := []byte{kind}
	if name != "" {
		p = append(append(p, byte(len(name))), name...)
	}
	p = append(p, body...)
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(p))), p...)
}

// fakeCharon is charon on a socket in a temporary directory, which it returns. It confirms every
// event's registration, and answers each command with events, list-sa events, then with response.
// requests gets the packet of each command.
func fakeCharon(t *testing.T, events []*vici.Message,
	response *vici.Message) (string, <-chan []byte) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "charon.vici")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() }) //nolint:errcheck // the test is over
	requests := make(chan []byte, 10)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close() //nolint:errcheck // the fake is done
		for serve(conn, events, response, requests) {
		}
	}()
	return socket, requests
}

// serve reads a packet from conn and answers it, as fakeCharon does; it returns whether conn is
// still open.
func serve(conn net.Conn, events []*vici.Message, response *vici.Message,
	requests chan<- []byte) bool {
	size := make([]byte, 4)
	if _, err := io.ReadFull(conn, size); err != nil {
		return false
	}
	p := make([]byte, binary.BigEndian.Uint32(size))
	if _, err := io.ReadFull(conn, p); err != nil {
		return false
	}
	var answer []byte
	switch p[0] {
	case eventRegister, eventUnregister:
		answer = packet(eventConfirm, "", nil)
	case cmdRequest:
		requests <- p
		for _, e := range events {
			answer = append(answer, packet(event, "list-sa", encode(e))...)
		}
		answer = append(answer, packet(cmdResponse, "", encode(response))...)
	}
	_, err := conn.Write(answer)
	return err == nil
}

// message is a VICI message of the keys and values in pairs, in their order.
func message(t *testing.T, pairs ...any) *vici.Message {
	t.Helper()
	m := vici.NewMessage()
	for i := 0; i < len(pairs); i += 2 {
		if err := m.Set(pairs[i].(string), pairs[i+1]); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

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
