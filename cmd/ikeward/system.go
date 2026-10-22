package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/install"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
	"github.com/zadykian/ikeward/internal/updown"
	"github.com/zadykian/ikeward/internal/watchdog"
)

// system is what ikeward's commands reach the system through; tests give fakes.
type system struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	// terminal is standard input's, where that is a terminal.
	terminal terminal
	files    install.Files
	runner   install.Runner
	// tunnel is strongSwan, network the kernel's addresses, links, rules and routes, and resolver
	// the remote network's DNS server, asked directly. names is the system's resolver.
	tunnel   status.Tunnel
	network  network
	resolver status.Resolver
	names    names
	// clock is the time, which the watchdog waits on.
	clock watchdog.Clock
	// route is the route through the tunnel's link, updownTunnel strongSwan as updown asks it,
	// and env the environment strongSwan runs updown in.
	route        updown.Route
	updownTunnel status.Tunnel
	env          func(key string) string
	// nat is ikeward's nftables table, which routing and the watchdog make, status reads and
	// uninstall removes.
	nat natTable
	// euid is ikeward's effective user ID.
	euid func() int
	// runsAs returns the program ikeward runs as, and whether that is the file at path, or the
	// one a link at path names.
	runsAs func(path string) (string, bool, error)
}

// network is the kernel's addresses, links, rules and routes, as each command reaches them.
type network interface {
	install.Network
	routing.Kernel
}

// natTable is routing.NATTable in nftables, as each command reaches it.
type natTable interface {
	routing.NAT
	install.Tables
	status.NAT
}

// names resolves host names, as the system's resolver does.
type names interface {
	// IPv4 returns name's IPv4 addresses, at least one, or an error that says why there is none.
	IPv4(ctx context.Context, name string) ([]netip.Addr, error)
}

// terminal is the terminal that standard input may be.
type terminal interface {
	// IsTerminal is whether standard input is a terminal.
	IsTerminal() bool
	// ReadPassword reads a line from the terminal, which does not echo it.
	ReadPassword() ([]byte, error)
}

// asRoot refuses command, which does what calls for root, unless ikeward runs as root.
func (s system) asRoot(command, does string) error {
	if s.euid() != 0 {
		return fmt.Errorf("%s %s, and must run as root", command, does)
	}
	return nil
}

// asBinary refuses an install unless ikeward runs as files.Binary, which the watchdog's service,
// the routing's and the connection's updown run. A dry run, which out shows, only says so.
func (s system) asBinary(out io.Writer, dryRun bool) error {
	running, same, err := s.runsAs(files.Binary)
	if err != nil {
		return fmt.Errorf("cannot tell whether ikeward runs as %s: %w", files.Binary, err)
	}
	if same {
		return nil
	}
	refusal := fmt.Sprintf("install must run as %s, which the watchdog's service, the routing's "+
		"and strongSwan run, not as %s", files.Binary, running)
	if !dryRun {
		return errors.New(refusal)
	}
	_, err = fmt.Fprintf(out, "Note: %s\n", refusal)
	return err
}

// maxPassword caps the line read from standard input for a password.
const maxPassword = 4096

// password returns the .p12's password: standard input's first line where that is no terminal,
// as a script gives it. On a terminal, the user types it at a prompt with no echo.
func (s system) password() (string, error) {
	if !s.terminal.IsTerminal() {
		return firstLine(s.stdin)
	}
	if _, err := fmt.Fprint(s.stderr, "Password of the profile's .p12: "); err != nil {
		return "", err
	}
	secret, err := s.terminal.ReadPassword()
	// The terminal did not echo the Enter that ended the password either.
	if _, printErr := fmt.Fprintln(s.stderr); err == nil {
		err = printErr
	}
	return string(secret), err
}

// firstLine is r's first line, without its line ending.
func firstLine(r io.Reader) (string, error) {
	line, err := bufio.NewReaderSize(r, maxPassword).ReadSlice('\n')
	switch {
	case errors.Is(err, bufio.ErrBufferFull):
		return "", fmt.Errorf("standard input's first line is over %d bytes", maxPassword)
	case errors.Is(err, io.EOF) && len(line) == 0:
		return "", errors.New("standard input, not a terminal, is empty")
	case err != nil && !errors.Is(err, io.EOF):
		return "", fmt.Errorf("cannot read standard input: %w", err)
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"), nil
}
