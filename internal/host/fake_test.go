package host

import (
	"testing"

	"github.com/google/nftables"
	"github.com/mdlayher/netlink"
	"github.com/mdlayher/netlink/nltest"
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
