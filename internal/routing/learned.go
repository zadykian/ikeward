package routing

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"slices"
	"strings"
)

// Learned records the ranges the VPN server granted, a range on each line, as ikeward
// updown learns them while the tunnel comes up. Each keeps a floor from boot on, as a floor of
// files.SettingsFile does. A line "server ADDRESS" records an address the VPN server had, which no
// floor may hold.
const Learned = "/var/lib/ikeward/learned-ranges"

// Reader reads the files under the system's root directory, each named by its absolute path.
type Reader interface {
	// Read returns the file at path and its permission bits; where there is none, an error that
	// wraps fs.ErrNotExist.
	Read(path string) ([]byte, fs.FileMode, error)
}

// Files are the files under the system's root directory, which updown writes too.
type Files interface {
	Reader
	// Write replaces the file at path with data and mode, so that no one reads it half written.
	Write(path string, data []byte, mode fs.FileMode) error
}

// Record is what Learned holds: the ranges updown learned, and the VPN server's addresses.
type Record struct {
	Ranges  []netip.Prefix
	Servers []netip.Addr
}

// serverPrefix starts a line of Learned that records a VPN server's address.
const serverPrefix = "server "

// ReadLearned returns what Learned records, in its order, and nothing without the file. A line
// that is neither an IPv4 range nor a server's address fails it. A range that Refusal refuses
// reads as any other, and Floors passes over it.
func ReadLearned(f Reader) (Record, error) {
	var r Record
	data, _, err := f.Read(Learned)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r, nil
	case err != nil:
		return r, fmt.Errorf("cannot read %s: %w", Learned, err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if address, ok := strings.CutPrefix(line, serverPrefix); ok {
			a, err := netip.ParseAddr(address)
			if err != nil {
				return Record{}, fmt.Errorf("%s: line %d: %q is no address", Learned, i+1, line)
			}
			r.Servers = append(r.Servers, a.Unmap())
			continue
		}
		p, err := netip.ParsePrefix(line)
		if err != nil || !p.Addr().Is4() || p != p.Masked() {
			return Record{}, fmt.Errorf("%s: line %d: %q is no IPv4 range", Learned, i+1, line)
		}
		r.Ranges = append(r.Ranges, p)
	}
	return r, nil
}

// Learn adds server and dst to Learned, unless the file holds them already, and returns whether
// it added dst. An invalid dst adds server alone. It writes the file anew, each part in order.
func Learn(f Files, server netip.Addr, dst netip.Prefix) (bool, error) {
	r, err := ReadLearned(f)
	if err != nil {
		return false, err
	}
	added := dst.IsValid() && !slices.Contains(r.Ranges, dst)
	newServer := server.IsValid() && !slices.Contains(r.Servers, server)
	if !added && !newServer {
		return false, nil
	}
	if added {
		r.Ranges = append(r.Ranges, dst)
	}
	if newServer {
		r.Servers = append(r.Servers, server)
	}
	slices.SortFunc(r.Ranges, netip.Prefix.Compare)
	slices.SortFunc(r.Servers, netip.Addr.Compare)
	var data bytes.Buffer
	data.WriteString("# The ranges the VPN server granted, which ikeward updown learned, and " +
		"the VPN server's addresses. ikeward routing makes each range unreachable below the " +
		"route through the tunnel, but one wider than /8 or holding a server's address.\n")
	for _, a := range r.Servers {
		data.WriteString(serverPrefix + a.String() + "\n")
	}
	for _, p := range r.Ranges {
		data.WriteString(p.String() + "\n")
	}
	if err := f.Write(Learned, data.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("cannot write %s: %w", Learned, err)
	}
	return added, nil
}

// Passed is a range of the record that Floors passes over, and Refusal's reason.
type Passed struct {
	Range netip.Prefix
	Why   string
}

// Floors are the ranges that take a floor: each of given, the settings' floors, then each of r's
// ranges, that Refusal allows with r's servers. Those it passes over go in passed. Each comes once.
// A floor of given keeps its traffic off the default route on a first boot, or once the record is
// lost.
func (r Record) Floors(given []netip.Prefix) (floors []netip.Prefix, passed []Passed) {
	for _, p := range slices.Concat(given, r.Ranges) {
		switch why := Refusal(p, r.Servers); {
		case why != "" && !slices.Contains(passed, Passed{p, why}):
			passed = append(passed, Passed{p, why})
		case why == "" && !slices.Contains(floors, p):
			floors = append(floors, p)
		}
	}
	return floors, passed
}
