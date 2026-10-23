package host

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"testing"

	"github.com/google/nftables"
	"github.com/mdlayher/netlink"
	"github.com/mdlayher/netlink/nltest"
	"github.com/strongswan/govici/vici"
	"golang.org/x/sys/unix"
)

// kernel answers nftables' requests in the kernel's place. It records each batch sent to it, and
// acks each message, or answers the batch with errno, or fails the socket with err. It records each
// request that reads too, and answers it from lists, by the nftables message the request sends,
// with the objects it lists. A message that lists lacks answers ENOENT, as for a missing table.
type kernel struct {
	batches [][]netlink.Message
	reads   []netlink.Message
	lists   map[int][]netlink.Message
	errno   unix.Errno
	err     error
}

func (k *kernel) answer(req []netlink.Message) ([]netlink.Message, error) {
	// A receive with nothing sent, after the acks: nothing more comes.
	if len(req) == 0 {
		return nil, nil
	}
	if req[0].Header.Type != unix.NFNL_MSG_BATCH_BEGIN {
		return k.list(req[0])
	}
	k.batches = append(k.batches, req)
	switch {
	case k.err != nil:
		return nil, k.err
	case k.errno != 0:
		return nltest.Error(int(k.errno), req[1:])
	}
	var acks []netlink.Message
	for _, m := range req {
		if m.Header.Flags&netlink.Acknowledge != 0 {
			acks = append(acks, netlink.Message{
				Header: netlink.Header{Type: netlink.Error, Sequence: m.Header.Sequence,
					PID: m.Header.PID},
				Data: make([]byte, 4),
			})
		}
	}
	return acks, nil
}

// list answers req, a request that reads, from k.lists. A dump's answer ends with a message that
// marks its end, as the kernel's does.
func (k *kernel) list(req netlink.Message) ([]netlink.Message, error) {
	k.reads = append(k.reads, req)
	listed, ok := k.lists[int(req.Header.Type&0xff)]
	switch {
	case k.err != nil:
		return nil, k.err
	case k.errno != 0:
		return nltest.Error(int(k.errno), []netlink.Message{req})
	case !ok:
		return nltest.Error(int(unix.ENOENT), []netlink.Message{req})
	}
	var out []netlink.Message
	for _, m := range listed {
		m.Header.Sequence = req.Header.Sequence
		out = append(out, m)
	}
	if req.Header.Flags&netlink.Dump == 0 {
		return out, nil
	}
	out = append(out, netlink.Message{Header: netlink.Header{Type: netlink.Done,
		Sequence: req.Header.Sequence}})
	for i := range out {
		out[i].Header.Flags |= netlink.Multi
	}
	return out, nil
}

// nat is NAT on k.
func (k *kernel) nat() NAT {
	return NAT{options: []nftables.ConnOption{nftables.WithTestDial(k.answer)},
		dial: func() (*netlink.Conn, error) { return nltest.Dial(k.answer), nil }}
}

// nft is the netlink message type that nftables' message msg has.
func nft(msg int) netlink.HeaderType {
	return netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | msg)
}

// types are the messages' types.
func types(batch []netlink.Message) []netlink.HeaderType {
	var out []netlink.HeaderType
	for _, m := range batch {
		out = append(out, m.Header.Type)
	}
	return out
}

// attribute is an attribute of a message: its type, the nested flag dropped, and its data.
type attribute struct {
	kind uint16
	data []byte
}

// attributes are the attributes in data, in order.
func attributes(t *testing.T, data []byte) []attribute {
	t.Helper()
	decoder, err := netlink.NewAttributeDecoder(data)
	if err != nil {
		t.Fatal(err)
	}
	var out []attribute
	for decoder.Next() {
		out = append(out, attribute{decoder.Type(), decoder.Bytes()})
	}
	if err := decoder.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// get is the data of the attribute of kind in attrs, where there is one.
func get(t *testing.T, attrs []attribute, kind uint16) []byte {
	t.Helper()
	for _, a := range attrs {
		if a.kind == kind {
			return a.data
		}
	}
	t.Fatalf("no attribute %d in %v", kind, attrs)
	return nil
}

// body is the attributes of an nftables message, past its family header, whose family must be
// IPv4's.
func body(t *testing.T, m netlink.Message) []attribute {
	t.Helper()
	if family := m.Data[0]; family != unix.NFPROTO_IPV4 {
		t.Errorf("message %v is of family %d, want IPv4's", m.Header.Type, family)
	}
	return attributes(t, m.Data[4:])
}

// name is s as nftables sends a name.
func name(s string) []byte { return append([]byte(s), 0) }

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
// event's registration, and answers each command with events, named as the last event registered,
// then with response. requests gets the packet of each command.
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
		var registered string
		for serve(conn, events, response, requests, &registered) {
		}
	}()
	return socket, requests
}

// serve reads a packet from conn and answers it, as fakeCharon does. It keeps the name of the
// last event registered in registered, and returns whether conn is still open.
func serve(conn net.Conn, events []*vici.Message, response *vici.Message,
	requests chan<- []byte, registered *string) bool {
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
	case eventRegister:
		*registered = string(p[2 : 2+p[1]])
		answer = packet(eventConfirm, "", nil)
	case eventUnregister:
		answer = packet(eventConfirm, "", nil)
	case cmdRequest:
		requests <- p
		for _, e := range events {
			answer = append(answer, packet(event, *registered, encode(e))...)
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
