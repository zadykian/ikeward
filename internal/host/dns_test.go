package host

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/status"
)

// question is the query for example.test's IPv4 addresses, after its ID and flags.
var question = []byte("\x00\x01\x00\x00\x00\x00\x00\x00" +
	"\x07example\x04test\x00\x00\x01\x00\x01")

// reply is a reply to query, with flags and an answer count of answers, each an address.
func reply(query []byte, flags uint16, answers int) []byte {
	r := append([]byte{}, query...)
	binary.BigEndian.PutUint16(r[2:], flags)
	binary.BigEndian.PutUint16(r[6:], uint16(answers))
	for range answers {
		r = append(r, "\xc0\x0c\x00\x01\x00\x01\x00\x00\x0e\x10\x00\x04\xc0\x00\x02\x14"...)
	}
	return r
}

// fakeServer is a DNS server on the far end of a pipe. It reads queries until the pipe closes,
// and records each. It drops the first drop of them. To each later one, it writes what replies
// makes of it after delay, in a goroutine of its own, so that it still reads the client's next
// send. Then it closes done.
type fakeServer struct {
	replies func(query []byte) [][]byte
	drop    int
	delay   time.Duration
	queries [][]byte
	times   []time.Time
	address string
	done    chan struct{}
}

// testResend is how long the DNS of fakeServer waits for a reply before it sends again.
const testResend = 20 * time.Millisecond

// dns is a DNS whose dial reaches s, or fails with err, and which sends again each testResend.
func (s *fakeServer) dns(t *testing.T, err error) DNS {
	t.Helper()
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		s.address = network + " " + address
		if err != nil {
			return nil, err
		}
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() }) //nolint:errcheck // the test is over
		s.done = make(chan struct{})
		go s.serve(server)
		return client, nil
	}
	return DNS{dial: dial, resend: testResend}
}

// serve answers the queries on conn, as fakeServer says, until conn closes.
func (s *fakeServer) serve(conn net.Conn) {
	var writes sync.WaitGroup
	defer close(s.done)
	defer writes.Wait()
	buf := make([]byte, 512)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		query := append([]byte{}, buf[:n]...)
		s.queries = append(s.queries, query)
		s.times = append(s.times, time.Now())
		if len(s.queries) <= s.drop {
			continue
		}
		writes.Go(func() {
			time.Sleep(s.delay)
			for _, r := range s.replies(query) {
				if _, err := conn.Write(r); err != nil {
					return
				}
			}
		})
	}
}

// answers is a server's replies: one with an address.
func answers(q []byte) [][]byte { return [][]byte{reply(q, 0x8180, 1)} }

// Lookup asks the server for the name's IPv4 addresses, over UDP, and passes only where the
// server answers the query with an address. It sends the query again each testResend, with the
// same ID, until the server answers one of them, or ctx ends. Its error wraps status.ErrAnswer
// where the server answers with an error code or no address, and not where it never answers.
func TestDNSLookup(t *testing.T) {
	const timeout = 200 * time.Millisecond
	for _, test := range []struct {
		name    string
		server  fakeServer
		want    string
		resends bool
	}{
		{"answers", fakeServer{replies: answers}, "", false},
		{"stale and stray replies", fakeServer{replies: func(q []byte) [][]byte {
			other := reply(q, 0x8180, 1)
			other[1]++
			return [][]byte{other, reply(q, 0x0100, 0), q[:11], reply(q, 0x8180, 2)}
		}}, "", false},
		{"NXDOMAIN", fakeServer{replies: func(q []byte) [][]byte {
			return [][]byte{reply(q, 0x8183, 0)}
		}}, "it answers NXDOMAIN", false},
		{"SERVFAIL", fakeServer{replies: func(q []byte) [][]byte {
			return [][]byte{reply(q, 0x8182, 0)}
		}}, "it answers SERVFAIL", false},
		{"other error", fakeServer{replies: func(q []byte) [][]byte {
			return [][]byte{reply(q, 0x8189, 0)}
		}}, "it answers with error code 9", false},
		{"no address", fakeServer{replies: func(q []byte) [][]byte {
			return [][]byte{reply(q, 0x8180, 0)}
		}}, "it answers with no address", false},
		{"drops the first query", fakeServer{replies: answers, drop: 1}, "", true},
		{"answers late", fakeServer{replies: answers, delay: 3 * testResend}, "", true},
		{"silent", fakeServer{replies: func([]byte) [][]byte { return nil }},
			"no answer in time", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := &test.server
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			err := server.dns(t, nil).Lookup(ctx, "192.0.2.10:53", "example.test")
			if got := errorText(err); got != test.want {
				t.Errorf("%q, want %q", got, test.want)
			}
			// A reply with an error code or no address came back, which no answer did not.
			answered := strings.HasPrefix(test.want, "it answers ")
			if got := errors.Is(err, status.ErrAnswer); got != answered {
				t.Errorf("wraps status.ErrAnswer: %v, want %v", got, answered)
			}
			<-server.done
			if server.address != "udp 192.0.2.10:53" {
				t.Errorf("dialled %s", server.address)
			}
			checkQueries(t, server.queries, test.resends, int(timeout/testResend)+2)
		})
	}
}

// checkQueries checks that the client sent the query for example.test, and sent it again where
// resends, each time with the same ID, and no more than limit times.
func checkQueries(t *testing.T, queries [][]byte, resends bool, limit int) {
	t.Helper()
	if len(queries) == 0 || len(queries[0]) < 4 || !bytes.Equal(queries[0][2:4], []byte{1, 0}) ||
		!bytes.Equal(queries[0][4:], question) {
		t.Fatalf("asked % x", queries)
	}
	for i, q := range queries[1:] {
		if !bytes.Equal(q, queries[0]) {
			t.Errorf("query %d is % x, not the first's % x", i+2, q, queries[0])
		}
	}
	if resends && len(queries) < 2 {
		t.Error("asked once, and not again")
	}
	if len(queries) > limit {
		t.Errorf("asked %d times, more than %d", len(queries), limit)
	}
}

// Lookup sends its query again a second after a send that no reply answers, where the DNS gives
// no other interval.
func TestDNSResendInterval(t *testing.T) {
	server := &fakeServer{replies: answers, drop: 1}
	dns := server.dns(t, nil)
	dns.resend = 0
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := dns.Lookup(ctx, "192.0.2.10:53", "example.test"); err != nil {
		t.Fatal(err)
	}
	<-server.done
	if len(server.times) != 2 {
		t.Fatalf("asked %d times, want 2", len(server.times))
	}
	// The server notes the first query's time a moment after the client's deadline starts.
	if gap := server.times[1].Sub(server.times[0]); gap < 900*time.Millisecond ||
		gap > 2*time.Second {
		t.Errorf("asked again after %v, want 1s", gap)
	}
}

// errorText is err's text, or none where err is nil.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Lookup fails, and says why, where it cannot reach the server, or the server's end closes, or ctx
// ends before the dial, or the name is no domain name.
func TestDNSLookupFails(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "udp", Err: errors.New("connection refused")}
	err := (&fakeServer{}).dns(t, refused).Lookup(t.Context(), "192.0.2.10:53", "example.test")
	if got := errorText(err); got != "connection refused" {
		t.Errorf("unreachable: %q", got)
	}

	// The server's end closes before the query, or after it.
	for want, after := range map[string]bool{"io: read/write on closed pipe": false, "EOF": true} {
		closing := DNS{dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				if after {
					_, _ = server.Read(make([]byte, 512)) //nolint:errcheck // the query, dropped
				}
				_ = server.Close() //nolint:errcheck // the fake is done
			}()
			return client, nil
		}}
		err := closing.Lookup(t.Context(), "192.0.2.10:53", "example.test")
		if got := errorText(err); got != want {
			t.Errorf("closed: %q, want %q", got, want)
		}
	}

	// With ctx ended, the default dialer opens no socket.
	ended, end := context.WithCancel(t.Context())
	end()
	err = DNS{}.Lookup(ended, "192.0.2.1:53", "example.test")
	if got := errorText(err); got != "operation was canceled" {
		t.Errorf("ended: %q", got)
	}

	for _, name := range []string{"", "example..test", strings.Repeat("a", 64) + ".test"} {
		err := DNS{}.Lookup(t.Context(), "192.0.2.1:53", name)
		if got, want := errorText(err), `"`+name+`" is not a domain name`; got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}

	// A trailing dot names the same domain.
	if q, err := dnsQuery(7, "example.test."); err != nil || !bytes.Equal(q[4:], question) {
		t.Errorf("% x, %v", q, err)
	}
}

// bindTo binds a socket to a link the kernel has, and fails, saying why, for one it lacks. A
// unixgram socket takes SO_BINDTODEVICE as a UDP one does, and reaches no network.
func TestBindTo(t *testing.T) {
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(t.TempDir(),
		"socket"), Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // the test is over
	raw, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	for link, want := range map[string]string{"lo": "",
		"ikeward-none0": "cannot bind to ikeward-none0: no such device"} {
		if got := errorText(bindTo(link)("unixgram", "", raw)); got != want {
			t.Errorf("%s: %q, want %q", link, got, want)
		}
	}
}

// Lookup stops as soon as ctx ends, as it reads a reply or as a send waits for a server that reads
// no more, and says why.
func TestDNSLookupEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	server := &fakeServer{replies: func([]byte) [][]byte { cancel(); return nil }}
	err := server.dns(t, nil).Lookup(ctx, "192.0.2.10:53", "example.test")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}

	// The server reads the first query alone, so the second send waits until ctx ends.
	stuck := DNS{resend: testResend, dial: func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() }) //nolint:errcheck // the test is over
		go func() {
			_, _ = server.Read(make([]byte, 512)) //nolint:errcheck // the query, dropped
		}()
		return client, nil
	}}
	timed, stop := context.WithTimeout(t.Context(), 5*testResend)
	defer stop()
	err = stuck.Lookup(timed, "192.0.2.10:53", "example.test")
	if got := errorText(err); got != "no answer in time" {
		t.Errorf("stuck: %q", got)
	}
}
