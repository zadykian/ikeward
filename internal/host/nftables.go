package host

import (
	"errors"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// NAT is routing.NATTable in the kernel's nftables, which it changes and reads through netlink.
// Each change is one transaction: no packet sees the table half made. It removes the table for
// uninstall. Connections, in natcheck.go, reads it.
type NAT struct {
	// options are nftables.New's, and dial opens the socket the reads go through. Tests give ones
	// that answer in the kernel's place.
	options []nftables.ConnOption
	dial    func() (*netlink.Conn, error)
}

// natTable is routing.NATTable, of the ip family.
var natTable = &nftables.Table{Name: routing.NATTable, Family: nftables.TableFamilyIPv4}

// Set makes the table hold its NAT chain and its rule alone. It adds the table where none exists,
// so that the delete that follows cannot fail, deletes it with all it holds, then makes it again,
// all in one transaction.
func (n NAT) Set() error {
	return apply(n.options, func(c *nftables.Conn) {
		c.AddTable(natTable)
		c.DelTable(natTable)
		c.AddTable(natTable)
		chain := c.AddChain(&nftables.Chain{Name: routing.NATChain, Table: natTable,
			Type: nftables.ChainTypeNAT, Hooknum: nftables.ChainHookPostrouting,
			Priority: nftables.ChainPriorityRef(routing.NATPriority)})
		c.AddRule(&nftables.Rule{Table: natTable, Chain: chain, Exprs: masquerade()})
	})
}

// HasTable is whether nftables has the table of the ip family named name.
func (n NAT) HasTable(name string) (bool, error) {
	conn, err := n.connect()
	if err != nil {
		return false, err
	}
	defer conn.Close() //nolint:errcheck // read only: closing loses nothing
	_, err = list(conn, unix.NFT_MSG_GETTABLE, false, named(unix.NFTA_TABLE_NAME, name))
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	return err == nil, err
}

// RemoveTable removes the table of the ip family named name, where it exists: it adds it first,
// so that the delete cannot fail.
func (n NAT) RemoveTable(name string) error {
	table := &nftables.Table{Name: name, Family: nftables.TableFamilyIPv4}
	return apply(n.options, func(c *nftables.Conn) {
		c.AddTable(table)
		c.DelTable(table)
	})
}

// connect opens the socket that reads go through, as dial gives it in tests.
func (n NAT) connect() (*netlink.Conn, error) {
	dial := n.dial
	if dial == nil {
		dial = func() (*netlink.Conn, error) { return netlink.Dial(unix.NETLINK_NETFILTER, nil) }
	}
	conn, err := dial()
	if err != nil {
		return nil, reason(err)
	}
	return conn, nil
}

// apply sends what batch queues as one transaction, on a connection that options give.
func apply(options []nftables.ConnOption, batch func(*nftables.Conn)) error {
	c, err := nftables.New(options...)
	if err != nil {
		return err
	}
	batch(c)
	err = c.Flush()
	// The operation netlink names says nothing to the user.
	if e, ok := errors.AsType[*netlink.OpError](err); ok {
		return reason(e.Err)
	}
	return err
}

// ifname is name as the kernel holds a link's name, in IFNAMSIZ bytes.
func ifname(name string) []byte {
	data := make([]byte, unix.IFNAMSIZ)
	copy(data, name)
	return data
}

// masquerade is routing.NATRule as nftables expressions. nft compares a link's name in the 16
// bytes of the kernel's IFNAMSIZ, its end padded with zeros. The counter counts the connections
// that took the rule: NAT sees their first packets alone.
func masquerade() []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(files.Link)},
		&expr.Counter{},
		&expr.Masq{},
	}
}
