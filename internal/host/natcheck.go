package host

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/nftables/expr"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"

	"github.com/zadykian/ikeward/internal/routing"
)

// Connections returns how many connections the rule has NATed since Set made it. It fails with
// routing.Missing where the table is missing, dormant, or holds more or other than Set makes.
func (n NAT) Connections() (uint64, error) {
	conn, err := n.connect()
	if err != nil {
		return 0, fmt.Errorf("cannot read table ip %s: %w", routing.NATTable, err)
	}
	defer conn.Close() //nolint:errcheck // read only: closing loses nothing
	tables, err := list(conn, unix.NFT_MSG_GETTABLE, false,
		named(unix.NFTA_TABLE_NAME, routing.NATTable))
	if errors.Is(err, unix.ENOENT) {
		return 0, routing.Missing{Err: fmt.Errorf("no table ip %s", routing.NATTable)}
	}
	if err != nil {
		return 0, fmt.Errorf("cannot read table ip %s: %w", routing.NATTable, err)
	}
	if err := natTableIs(tables); err != nil {
		return 0, err
	}
	chains, err := list(conn, unix.NFT_MSG_GETCHAIN, false,
		named(unix.NFTA_CHAIN_TABLE, routing.NATTable), named(unix.NFTA_CHAIN_NAME, routing.NATChain))
	if errors.Is(err, unix.ENOENT) {
		return 0, routing.Missing{Err: fmt.Errorf("table ip %s has no chain %s", routing.NATTable,
			routing.NATChain)}
	}
	if err != nil {
		return 0, fmt.Errorf("cannot read chain %s of table ip %s: %w", routing.NATChain,
			routing.NATTable, err)
	}
	if err := natChain(chains); err != nil {
		return 0, err
	}
	rules, err := list(conn, unix.NFT_MSG_GETRULE, true,
		named(unix.NFTA_RULE_TABLE, routing.NATTable), named(unix.NFTA_RULE_CHAIN, routing.NATChain))
	if err != nil {
		return 0, fmt.Errorf("cannot read the rules of table ip %s: %w", routing.NATTable, err)
	}
	return natRule(rules)
}

// natTableIs fails unless tables is the one table Set makes, as nftables lists it. A dormant table
// hooks no chain. A table's use counts what it holds, as chains and sets: one, its chain.
func natTableIs(tables []map[uint16][]byte) error {
	ok := len(tables) == 1
	if ok {
		ok = number(tables[0][unix.NFTA_TABLE_FLAGS])&unix.NFT_TABLE_F_DORMANT == 0 &&
			number(tables[0][unix.NFTA_TABLE_USE]) == 1
	}
	if !ok {
		return routing.Missing{Err: fmt.Errorf("table ip %s is not ikeward's: it must hold "+
			"chain %s alone, and not be dormant", routing.NATTable, routing.NATChain)}
	}
	return nil
}

// accept is the kernel's NF_ACCEPT, a chain's policy that lets through what no rule drops.
const accept = 1

// natChain fails unless chains is the one chain Set makes, as nftables lists it. That is of type
// nat, on the postrouting hook at routing.NATPriority, and accepts what its rule lets through.
func natChain(chains []map[uint16][]byte) error {
	ok := len(chains) == 1
	if ok {
		hook, err := decode(chains[0][unix.NFTA_CHAIN_HOOK])
		policy, hasPolicy := chains[0][unix.NFTA_CHAIN_POLICY]
		ok = err == nil && string(chains[0][unix.NFTA_CHAIN_TYPE]) == "nat\x00" &&
			number(hook[unix.NFTA_HOOK_HOOKNUM]) == unix.NF_INET_POST_ROUTING &&
			number(hook[unix.NFTA_HOOK_PRIORITY]) == routing.NATPriority &&
			(!hasPolicy || number(policy) == accept)
	}
	if !ok {
		return routing.Missing{Err: fmt.Errorf("chain %s of table ip %s is not ikeward's: type "+
			"nat, hook postrouting, priority %d, policy accept", routing.NATChain,
			routing.NATTable, routing.NATPriority)}
	}
	return nil
}

// natRule returns the packets that the counter of rules has counted, where rules are the one rule
// Set makes, as nftables lists it.
func natRule(rules []map[uint16][]byte) (uint64, error) {
	if len(rules) != 1 {
		return 0, routing.Missing{Err: fmt.Errorf("chain %s of table ip %s holds %d rules, not "+
			"its one: %s", routing.NATChain, routing.NATTable, len(rules), routing.NATRule)}
	}
	exprs, err := expressions(rules[0][unix.NFTA_RULE_EXPRESSIONS])
	var packets uint64
	for i, e := range exprs {
		if counter, ok := e.(*expr.Counter); ok {
			packets = counter.Packets
			exprs[i] = &expr.Counter{}
		}
	}
	if err != nil || !reflect.DeepEqual(exprs, masquerade()) {
		return 0, routing.Missing{Err: fmt.Errorf("the rule of table ip %s is not %s",
			routing.NATTable, routing.NATRule)}
	}
	return packets, nil
}

// expressions are those of data, a rule's as nftables lists them.
func expressions(data []byte) ([]expr.Any, error) {
	decoder, err := netlink.NewAttributeDecoder(data)
	if err != nil {
		return nil, err
	}
	var exprs []expr.Any
	for decoder.Next() {
		attrs, err := decode(decoder.Bytes())
		if err != nil {
			return nil, err
		}
		e, err := expression(string(bytes.TrimSuffix(attrs[unix.NFTA_EXPR_NAME], []byte{0})),
			attrs[unix.NFTA_EXPR_DATA])
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
	}
	return exprs, decoder.Err()
}

// expression is the expression of name with data, as nftables lists it. One of a kind that the
// NAT rule lacks fails it.
func expression(name string, data []byte) (expr.Any, error) {
	var e expr.Any
	switch name {
	case "meta":
		e = &expr.Meta{}
	case "cmp":
		e = &expr.Cmp{}
	case "counter":
		e = &expr.Counter{}
	case "masq":
		e = &expr.Masq{}
	default:
		return nil, fmt.Errorf("an expression %q", name)
	}
	return e, expr.Unmarshal(unix.NFPROTO_IPV4, data, e)
}

// list asks nftables, through conn, for the objects of the ip family that attrs name, of the kind
// that msg reads, and all those they match where dump. It returns the attributes of each.
func list(conn *netlink.Conn, msg int, dump bool, attrs ...netlink.Attribute) (
	[]map[uint16][]byte, error) {
	data, err := netlink.MarshalAttributes(attrs)
	if err != nil {
		return nil, err
	}
	flags := netlink.Request
	if dump {
		flags |= netlink.Dump
	}
	replies, err := conn.Execute(netlink.Message{
		Header: netlink.Header{Type: netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | msg),
			Flags: flags},
		// nftables' header: the family, the version, and a resource ID of 0.
		Data: append([]byte{unix.NFPROTO_IPV4, unix.NFNETLINK_V0, 0, 0}, data...),
	})
	// The operation netlink names says nothing to the user.
	if e, ok := errors.AsType[*netlink.OpError](err); ok {
		return nil, reason(e.Err)
	}
	if err != nil {
		return nil, err
	}
	var objects []map[uint16][]byte
	for _, m := range replies {
		if len(m.Data) < 4 {
			return nil, errors.New("nftables answered with a message too short")
		}
		object, err := decode(m.Data[4:])
		if err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, nil
}

// named is the attribute of kind that holds s, as nftables sends a name.
func named(kind uint16, s string) netlink.Attribute {
	return netlink.Attribute{Type: kind, Data: append([]byte(s), 0)}
}

// decode is the attributes in data, by their type, the nested flag dropped.
func decode(data []byte) (map[uint16][]byte, error) {
	decoder, err := netlink.NewAttributeDecoder(data)
	if err != nil {
		return nil, err
	}
	attrs := map[uint16][]byte{}
	for decoder.Next() {
		attrs[decoder.Type()] = decoder.Bytes()
	}
	return attrs, decoder.Err()
}

// number is data, a 32-bit number in network byte order, or -1 where data is not 4 bytes long.
func number(data []byte) int64 {
	if len(data) != 4 {
		return -1
	}
	return int64(int32(binary.BigEndian.Uint32(data)))
}
