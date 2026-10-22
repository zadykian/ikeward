package host

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/zadykian/ikeward/internal/status"
)

// DNS asks a DNS server directly, over UDP: not through systemd-resolved, nor /etc/hosts, which
// Go's resolver reads first, so that only the server's answer counts.
type DNS struct {
	// Link names the link the queries leave by, whatever the routes say, where it names one.
	Link string
	// dial connects to a server; nil is net.Dialer's, bound to Link. Tests give a pipe.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
	// resend is how long a send waits for its reply; zero is resendInterval. Tests give less.
	resend time.Duration
}

// resendInterval is how long Lookup waits for a reply before it sends its query again. A single
// send would fail the resolver check for one lost query or reply. The watchdog would then restart
// a working tunnel, about 3 times a day at 0.5% loss. The check's 5 seconds hold 5 sends.
const resendInterval = time.Second

// DNS's header bits and codes, as RFC 1035 gives them.
const (
	headerLen        = 12
	flagResponse     = 0x8000
	flagRecursion    = 0x0100
	rcodeMask        = 0x000f
	typeA, classIN   = 1, 1
	maxLabel, maxMsg = 63, 512
)

// rcodes name the errors a server may answer with.
var rcodes = map[uint16]string{
	1: "FORMERR", 2: "SERVFAIL", 3: "NXDOMAIN", 4: "NOTIMP", 5: "REFUSED",
}

// Lookup asks the server at server, an address and port, for name's IPv4 addresses, and fails
// unless it answers with at least one. It sends the query again each resendInterval, with the
// same ID, until a reply to any of the sends answers it or ctx ends.
func (d DNS) Lookup(ctx context.Context, server, name string) error {
	query, err := dnsQuery(uint16(rand.Uint32()), name)
	if err != nil {
		return err
	}
	dial := d.dial
	if dial == nil {
		dialer := &net.Dialer{}
		if d.Link != "" {
			dialer.Control = bindTo(d.Link)
		}
		dial = dialer.DialContext
	}
	conn, err := dial(ctx, "udp", server)
	if err != nil {
		return reason(err)
	}
	defer conn.Close() //nolint:errcheck // nothing was written that closing could lose
	// Reads and writes stop as soon as ctx ends, as at its deadline.
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now()) //nolint:errcheck // the read fails either way
	})
	defer stop()
	wait := d.resend
	if wait == 0 {
		wait = resendInterval
	}
	buf := make([]byte, maxMsg)
	for {
		if err := send(ctx, conn, query, wait); err != nil {
			return err
		}
		if err := answer(ctx, conn, query, buf); !errors.Is(err, errResend) {
			return err
		}
	}
}

// bindTo binds a socket to link before it connects, with SO_BINDTODEVICE, which Linux allows
// without CAP_NET_RAW from 5.7 on. What the socket sends then leaves by link alone. With no route
// there, the kernel takes the address for one on link, rather than send it by the default route.
func bindTo(link string) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var err error
		if controlErr := c.Control(func(fd uintptr) {
			err = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, link)
		}); controlErr != nil {
			return controlErr
		}
		if err != nil {
			return fmt.Errorf("cannot bind to %s: %w", link, err)
		}
		return nil
	}
}

// errResend is answer's error where no reply answers the query before the next send is due.
var errResend = errors.New("no answer before the next send")

// send writes query to conn, and sets conn's read deadline to the next send, wait from now. It
// checks ctx last, as ctx's end may have set the deadline to now before this one replaced it.
func send(ctx context.Context, conn net.Conn, query []byte, wait time.Duration) error {
	_, err := conn.Write(query)
	if err == nil {
		_ = conn.SetReadDeadline(time.Now().Add(wait)) //nolint:errcheck // a closed conn's read fails
	}
	if end := ended(ctx); end != nil {
		return end
	}
	return reason(err)
}

// ended is ctx's error once it has ended, and says that no answer came where its deadline passed.
func ended(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("no answer in time")
	}
	return ctx.Err()
}

// answer reads conn's replies into buf until one answers query, and says whether it gives an
// address, wrapping status.ErrAnswer where it gives none. Replies to other queries, as a late one
// to an earlier query, are skipped. Where the read's deadline passes and ctx goes on, it returns
// errResend.
func answer(ctx context.Context, conn net.Conn, query, buf []byte) error {
	for {
		n, err := conn.Read(buf)
		if end := ended(ctx); end != nil {
			return end
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return errResend
		}
		if err != nil {
			return reason(err)
		}
		reply := buf[:n]
		if n < headerLen || reply[0] != query[0] || reply[1] != query[1] {
			continue
		}
		flags := binary.BigEndian.Uint16(reply[2:])
		if flags&flagResponse == 0 {
			continue
		}
		if rcode := flags & rcodeMask; rcode != 0 {
			if name, ok := rcodes[rcode]; ok {
				return fmt.Errorf("%w %s", status.ErrAnswer, name)
			}
			return fmt.Errorf("%w with error code %d", status.ErrAnswer, rcode)
		}
		if binary.BigEndian.Uint16(reply[6:]) == 0 {
			return fmt.Errorf("%w with no address", status.ErrAnswer)
		}
		return nil
	}
}

// dnsQuery is a query, with id, for name's IPv4 addresses, which asks the server to recurse.
func dnsQuery(id uint16, name string) ([]byte, error) {
	q := binary.BigEndian.AppendUint16(nil, id)
	q = binary.BigEndian.AppendUint16(q, flagRecursion)
	// One question, and no records.
	q = append(q, 0, 1, 0, 0, 0, 0, 0, 0)
	for label := range strings.SplitSeq(strings.TrimSuffix(name, "."), ".") {
		if label == "" || len(label) > maxLabel {
			return nil, fmt.Errorf("%q is not a domain name", name)
		}
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0)
	q = binary.BigEndian.AppendUint16(q, typeA)
	return binary.BigEndian.AppendUint16(q, classIN), nil
}
