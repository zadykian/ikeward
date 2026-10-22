package host

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"

	"github.com/google/nftables/expr"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"

	"github.com/zadykian/ikeward/internal/routing"
)

// Connections reads back what Set made, its counter at 0. It reads the table, which the kernel
// lists with its flags and use, the chain, then the chain's rules, by name, the rules in a dump.
func TestConnections(t *testing.T) {
	k := &kernel{}
	if err := k.nat().Set(); err != nil {
		t.Fatal(err)
	}
	made := k.batches[0]
	k.lists = map[int][]netlink.Message{unix.NFT_MSG_GETTABLE: {table(t, 0, 1)},
		unix.NFT_MSG_GETCHAIN: {made[4]}, unix.NFT_MSG_GETRULE: {made[5]}}
	if connections, err := k.nat().Connections(); err != nil || connections != 0 {
		t.Fatalf("Connections: %d, %v; want 0", connections, err)
	}
	want := []netlink.HeaderType{nft(unix.NFT_MSG_GETTABLE), nft(unix.NFT_MSG_GETCHAIN),
		nft(unix.NFT_MSG_GETRULE)}
	if got := types(k.reads); !slices.Equal(got, want) {
		t.Fatalf("read %v, want %v", got, want)
	}
	for i, m := range k.reads {
		if dump := m.Header.Flags&netlink.Dump != 0; dump != (i == 2) {
			t.Errorf("read %v as a dump: %v", m.Header.Type, dump)
		}
		// The first attribute of each names the table: NFTA_TABLE_NAME, NFTA_CHAIN_TABLE and
		// NFTA_RULE_TABLE are all 1.
		if got := get(t, body(t, m), 1); !slices.Equal(got, name("ikeward")) {
			t.Errorf("read %v of table %q, want ikeward", m.Header.Type, got)
		}
	}
	if got := get(t, body(t, k.reads[1]), unix.NFTA_CHAIN_NAME); !slices.Equal(got,
		name("postrouting")) {
		t.Errorf("read the chain %q, want postrouting", got)
	}
	if got := get(t, body(t, k.reads[2]), unix.NFTA_RULE_CHAIN); !slices.Equal(got,
		name("postrouting")) {
		t.Errorf("read the rules of chain %q, want postrouting", got)
	}

	// The kernel lists the policy, the counter's packets and bytes, and the rule's handle too.
	k = &kernel{lists: lists(t, listedChain(t), counted(7))}
	if connections, err := k.nat().Connections(); err != nil || connections != 7 {
		t.Errorf("Connections: %d, %v; want 7", connections, err)
	}
}

// connectionsError is a case of Connections failing: the kernel it reads, the error it must
// return, and whether that is a routing.Missing.
type connectionsError struct {
	name    string
	kernel  *kernel
	want    string
	missing bool
}

// failConnections checks that Connections fails on each case's kernel as the case says.
func failConnections(t *testing.T, cases []connectionsError) {
	t.Helper()
	for _, test := range cases {
		connections, err := test.kernel.nat().Connections()
		if err == nil || err.Error() != test.want || connections != 0 {
			t.Errorf("%s: %d, %v; want %s", test.name, connections, err, test.want)
		}
		if missing := errors.As(err, &routing.Missing{}); missing != test.missing {
			t.Errorf("%s: %v is a routing.Missing: %v, want %v", test.name, err, missing,
				test.missing)
		}
	}
}

// Connections fails with routing.Missing where the table or its chain is missing or not what Set
// makes. Where nftables cannot be read, its error is no routing.Missing: the table may be there.
func TestConnectionsErrors(t *testing.T) {
	const notTable = "table ip ikeward is not ikeward's: it must hold chain postrouting alone, " +
		"and not be dormant"
	const notChain = "chain postrouting of table ip ikeward is not ikeward's: type nat, hook " +
		"postrouting, priority 99, policy accept"
	postrouting := uint32(unix.NF_INET_POST_ROUTING)
	failConnections(t, []connectionsError{
		{"no table", &kernel{}, "no table ip ikeward", true},
		{"no chain", &kernel{lists: map[int][]netlink.Message{
			unix.NFT_MSG_GETTABLE: {table(t, 0, 1)}}},
			"table ip ikeward has no chain postrouting", true},
		// A dormant table hooks none of its chains, and lets every packet through.
		{"dormant", &kernel{lists: tabled(t, unix.NFT_TABLE_F_DORMANT, 1)}, notTable, true},
		{"two chains", &kernel{lists: tabled(t, 0, 2)}, notTable, true},
		{"no use", &kernel{lists: tabled(t, 0, 0)}, notTable, true},
		{"after Docker's", &kernel{lists: lists(t, chain(t, "nat", postrouting, 100, 1),
			counted(0))}, notChain, true},
		{"on the output hook", &kernel{lists: lists(t, chain(t, "nat",
			unix.NF_INET_LOCAL_OUT, 99, 1), counted(0))}, notChain, true},
		{"of type filter", &kernel{lists: lists(t, chain(t, "filter", postrouting, 99, 1),
			counted(0))}, notChain, true},
		{"policy drop", &kernel{lists: lists(t, chain(t, "nat", postrouting, 99, 0),
			counted(0))}, notChain, true},
		// nfnetlink refuses a process without CAP_NET_ADMIN, as status run by another user.
		{"not permitted", &kernel{errno: unix.EPERM},
			"cannot read table ip ikeward: operation not permitted", false},
		{"socket", &kernel{err: unix.EPERM},
			"cannot read table ip ikeward: operation not permitted", false},
	})
	_, err := NAT{dial: func() (*netlink.Conn, error) { return nil, unix.EACCES }}.Connections()
	if !errors.Is(err, unix.EACCES) || errors.As(err, &routing.Missing{}) ||
		err.Error() != "cannot read table ip ikeward: permission denied" {
		t.Errorf("Connections with a socket that cannot be opened: %v, want %v alone", err,
			unix.EACCES)
	}
}

// Connections fails with routing.Missing where the chain holds another rule than Set makes, as an
// SNAT rule, or more.
func TestConnectionsRuleErrors(t *testing.T) {
	other := slices.Clone(counted(3))
	other[1] = &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname("eth0")}
	notOut := slices.Clone(counted(3))
	notOut[1] = &expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: ifname("ikeward0")}
	snat := []expr.Any{&expr.Counter{}, &expr.Immediate{Register: 1, Data: []byte{203, 0, 113, 1}},
		&expr.NAT{Type: expr.NATTypeSourceNAT, Family: unix.NFPROTO_IPV4, RegAddrMin: 1}}
	const notRule = "the rule of table ip ikeward is not " + routing.NATRule
	failConnections(t, []connectionsError{
		{"no rule", &kernel{lists: lists(t, listedChain(t))}, "chain postrouting of table ip " +
			"ikeward holds 0 rules, not its one: " + routing.NATRule, true},
		{"two rules", &kernel{lists: lists(t, listedChain(t), counted(0), counted(0))}, "chain " +
			"postrouting of table ip ikeward holds 2 rules, not its one: " + routing.NATRule,
			true},
		{"another link", &kernel{lists: lists(t, listedChain(t), other)}, notRule, true},
		{"every other link", &kernel{lists: lists(t, listedChain(t), notOut)}, notRule, true},
		{"no oifname", &kernel{lists: lists(t, listedChain(t), counted(0)[2:])}, notRule, true},
		{"snat", &kernel{lists: lists(t, listedChain(t), snat)}, notRule, true},
		{"random", &kernel{lists: lists(t, listedChain(t), append(counted(0)[:3],
			&expr.Masq{Random: true}))}, notRule, true},
	})
}

// counted is masquerade's expressions, its counter at packets, of 60 bytes each, a SYN's.
func counted(packets uint64) []expr.Any {
	exprs := masquerade()
	exprs[2] = &expr.Counter{Packets: packets, Bytes: 60 * packets}
	return exprs
}

// table is what the kernel lists of ikeward's NAT table, with flags, holding use chains and sets.
func table(t *testing.T, flags, use uint32) netlink.Message {
	t.Helper()
	return object(t, unix.NFT_MSG_NEWTABLE, named(unix.NFTA_TABLE_NAME, "ikeward"),
		netlink.Attribute{Type: unix.NFTA_TABLE_FLAGS, Data: be32(flags)},
		netlink.Attribute{Type: unix.NFTA_TABLE_USE, Data: be32(use)})
}

// tabled is what the kernel lists of ikeward's NAT table, with flags, holding use chains and sets,
// its chain and rule among them.
func tabled(t *testing.T, flags, use uint32) map[int][]netlink.Message {
	t.Helper()
	listed := lists(t, listedChain(t), counted(0))
	listed[unix.NFT_MSG_GETTABLE] = []netlink.Message{table(t, flags, use)}
	return listed
}

// listedChain is the attributes the kernel lists of ikeward's NAT chain, as Set makes it.
func listedChain(t *testing.T) []netlink.Attribute {
	t.Helper()
	return chain(t, "nat", unix.NF_INET_POST_ROUTING, 99, 1)
}

// chain is the attributes the kernel lists of ikeward's NAT chain, of type kind, on hook at
// priority, with policy.
func chain(t *testing.T, kind string, hook uint32, priority int32,
	policy uint32) []netlink.Attribute {
	t.Helper()
	return []netlink.Attribute{named(unix.NFTA_CHAIN_TABLE, "ikeward"),
		named(unix.NFTA_CHAIN_NAME, "postrouting"),
		nested(t, unix.NFTA_CHAIN_HOOK,
			netlink.Attribute{Type: unix.NFTA_HOOK_HOOKNUM, Data: be32(hook)},
			netlink.Attribute{Type: unix.NFTA_HOOK_PRIORITY, Data: be32(uint32(priority))}),
		{Type: unix.NFTA_CHAIN_POLICY, Data: be32(policy)}, named(unix.NFTA_CHAIN_TYPE, kind)}
}

// lists are what the kernel lists of ikeward's NAT table, which holds a chain with chain's
// attributes, and rules, each of its expressions.
func lists(t *testing.T, chain []netlink.Attribute, rules ...[]expr.Any) map[int][]netlink.Message {
	t.Helper()
	listed := map[int][]netlink.Message{
		unix.NFT_MSG_GETTABLE: {table(t, 0, 1)},
		unix.NFT_MSG_GETCHAIN: {object(t, unix.NFT_MSG_NEWCHAIN, chain...)},
		unix.NFT_MSG_GETRULE:  nil,
	}
	for i, rule := range rules {
		var elems []netlink.Attribute
		for _, e := range rule {
			data, err := expr.Marshal(unix.NFPROTO_IPV4, e)
			if err != nil {
				t.Fatal(err)
			}
			elems = append(elems, netlink.Attribute{Type: unix.NLA_F_NESTED | unix.NFTA_LIST_ELEM,
				Data: data})
		}
		listed[unix.NFT_MSG_GETRULE] = append(listed[unix.NFT_MSG_GETRULE],
			object(t, unix.NFT_MSG_NEWRULE, named(unix.NFTA_RULE_TABLE, "ikeward"),
				named(unix.NFTA_RULE_CHAIN, "postrouting"),
				nested(t, unix.NFTA_RULE_EXPRESSIONS, elems...),
				netlink.Attribute{Type: unix.NFTA_RULE_HANDLE,
					Data: binary.BigEndian.AppendUint64(nil, uint64(i+2))}))
	}
	return listed
}

// object is an nftables message of type msg, of the ip family, which holds attrs.
func object(t *testing.T, msg int, attrs ...netlink.Attribute) netlink.Message {
	t.Helper()
	data, err := netlink.MarshalAttributes(attrs)
	if err != nil {
		t.Fatal(err)
	}
	return netlink.Message{Header: netlink.Header{Type: nft(msg)},
		Data: append([]byte{unix.NFPROTO_IPV4, unix.NFNETLINK_V0, 0, 0}, data...)}
}

// nested is attrs in one attribute of kind, flagged nested.
func nested(t *testing.T, kind uint16, attrs ...netlink.Attribute) netlink.Attribute {
	t.Helper()
	data, err := netlink.MarshalAttributes(attrs)
	if err != nil {
		t.Fatal(err)
	}
	return netlink.Attribute{Type: unix.NLA_F_NESTED | kind, Data: data}
}

// be32 is n in network byte order.
func be32(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }
