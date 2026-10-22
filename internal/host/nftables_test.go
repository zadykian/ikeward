package host

import (
	"encoding/binary"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/google/nftables/expr"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// Set sends one batch, one transaction: the table added, deleted and made again, its NAT chain on
// postrouting at priority 99, and the rule.
func TestNATSet(t *testing.T) {
	k := &kernel{}
	if err := k.nat().Set(); err != nil {
		t.Fatal(err)
	}
	want := []netlink.HeaderType{unix.NFNL_MSG_BATCH_BEGIN, nft(unix.NFT_MSG_NEWTABLE),
		nft(unix.NFT_MSG_DELTABLE), nft(unix.NFT_MSG_NEWTABLE), nft(unix.NFT_MSG_NEWCHAIN),
		nft(unix.NFT_MSG_NEWRULE), unix.NFNL_MSG_BATCH_END}
	if len(k.batches) != 1 || !slices.Equal(types(k.batches[0]), want) {
		t.Fatalf("sent %v, want one batch of %v", k.batches, want)
	}
	batch := k.batches[0]
	for _, m := range batch[1:4] {
		if got := get(t, body(t, m), unix.NFTA_TABLE_NAME); !slices.Equal(got, name("ikeward")) {
			t.Errorf("a table message names %q, want ikeward", got)
		}
	}
	chain := body(t, batch[4])
	for kind, want := range map[uint16][]byte{unix.NFTA_CHAIN_TABLE: name("ikeward"),
		unix.NFTA_CHAIN_NAME: name("postrouting"), unix.NFTA_CHAIN_TYPE: name("nat")} {
		if got := get(t, chain, kind); !slices.Equal(got, want) {
			t.Errorf("the chain's attribute %d is %q, want %q", kind, got, want)
		}
	}
	hook := attributes(t, get(t, chain, unix.NFTA_CHAIN_HOOK))
	if got := binary.BigEndian.Uint32(get(t, hook, unix.NFTA_HOOK_HOOKNUM)); got !=
		unix.NF_INET_POST_ROUTING {
		t.Errorf("the chain's hook is %d, want postrouting's", got)
	}
	if got := int32(binary.BigEndian.Uint32(get(t, hook, unix.NFTA_HOOK_PRIORITY))); got != 99 {
		t.Errorf("the chain's priority is %d, want 99", got)
	}
	rule := body(t, batch[5])
	if got := get(t, rule, unix.NFTA_RULE_CHAIN); !slices.Equal(got, name("postrouting")) {
		t.Errorf("the rule's chain is %q", got)
	}
	var names []string
	for _, e := range attributes(t, get(t, rule, unix.NFTA_RULE_EXPRESSIONS)) {
		names = append(names, string(get(t, attributes(t, e.data), unix.NFTA_EXPR_NAME)))
	}
	wantNames := []string{"meta\x00", "cmp\x00", "counter\x00", "masq\x00"}
	if !slices.Equal(names, wantNames) {
		t.Errorf("the rule's expressions are %q, want %q", names, wantNames)
	}
}

// The rule, as nft lists it: oifname "ikeward0" counter masquerade. oifname loads the device's
// name, 16 bytes, as nft compares it. masquerade takes no flags and no ports.
func TestMasquerade(t *testing.T) {
	want := []expr.Any{
		&expr.Meta{Key: unix.NFT_META_OIFNAME, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1,
			Data: []byte{'i', 'k', 'e', 'w', 'a', 'r', 'd', '0', 0, 0, 0, 0, 0, 0, 0, 0}},
		&expr.Counter{},
		&expr.Masq{},
	}
	if got := masquerade(); !reflect.DeepEqual(got, want) {
		t.Errorf("masquerade:\n%#v\nwant:\n%#v", got, want)
	}
}

// HasTable asks for the table by name, not in a dump. The kernel's ENOENT says there is none.
func TestHasTable(t *testing.T) {
	k := &kernel{lists: map[int][]netlink.Message{unix.NFT_MSG_GETTABLE: {table(t, 0, 1)}}}
	if has, err := k.nat().HasTable("ikeward"); !has || err != nil {
		t.Errorf("HasTable: %v, %v; want true", has, err)
	}
	if len(k.reads) != 1 || k.reads[0].Header.Flags&netlink.Dump != 0 ||
		!slices.Equal(get(t, body(t, k.reads[0]), unix.NFTA_TABLE_NAME), name("ikeward")) {
		t.Errorf("read %v, want table ip ikeward alone", k.reads)
	}
	if has, err := (&kernel{}).nat().HasTable("other"); has || err != nil {
		t.Errorf("HasTable with no table: %v, %v; want false", has, err)
	}
	for _, k := range []*kernel{{errno: unix.EPERM}, {err: unix.EPERM}} {
		if has, err := k.nat().HasTable("ikeward"); has || !errors.Is(err, unix.EPERM) {
			t.Errorf("HasTable refused: %v, %v; want %v", has, err, unix.EPERM)
		}
	}
	_, err := NAT{dial: func() (*netlink.Conn, error) { return nil, unix.EACCES }}.HasTable("x")
	if !errors.Is(err, unix.EACCES) {
		t.Errorf("HasTable with a socket that cannot be opened: %v, want %v", err, unix.EACCES)
	}
}

// RemoveTable sends one batch: the table named added, so that the delete cannot fail, and
// deleted.
func TestRemoveTable(t *testing.T) {
	for _, table := range []string{"ikeward", "other"} {
		k := &kernel{}
		if err := k.nat().RemoveTable(table); err != nil {
			t.Fatal(err)
		}
		want := []netlink.HeaderType{unix.NFNL_MSG_BATCH_BEGIN, nft(unix.NFT_MSG_NEWTABLE),
			nft(unix.NFT_MSG_DELTABLE), unix.NFNL_MSG_BATCH_END}
		if len(k.batches) != 1 || !slices.Equal(types(k.batches[0]), want) {
			t.Fatalf("%s: sent %v, want one batch of %v", table, k.batches, want)
		}
		for _, m := range k.batches[0][1:3] {
			if got := get(t, body(t, m), unix.NFTA_TABLE_NAME); !slices.Equal(got, name(table)) {
				t.Errorf("a table message names %q, want %s", got, table)
			}
		}
	}
}

// A change that nftables refuses fails with the kernel's error alone, whether the socket fails or
// the kernel answers the batch with an error.
func TestNATErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		kernel *kernel
		change func(NAT) error
		want   string
		errno  unix.Errno
	}{
		{"not permitted", &kernel{err: unix.EPERM}, NAT.Set, "operation not permitted",
			unix.EPERM},
		{"answered", &kernel{errno: unix.EINVAL},
			func(n NAT) error { return n.RemoveTable("other") }, "invalid argument",
			unix.EINVAL},
	} {
		err := test.change(test.kernel.nat())
		if err == nil || err.Error() != test.want || !errors.Is(err, test.errno) {
			t.Errorf("%s: %v, want %s", test.name, err, test.want)
		}
		if len(test.kernel.batches) != 1 {
			t.Errorf("%s: sent %d batches, want 1", test.name, len(test.kernel.batches))
		}
	}
}
