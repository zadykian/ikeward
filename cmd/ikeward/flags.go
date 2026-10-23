package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/profile"
	"github.com/zadykian/ikeward/internal/routing"
)

// profileFlags are install's flags that name the profile's files.
type profileFlags struct {
	p12, sswan, mobileconfig string
}

// read reads the files f names, each up to a byte past profile.MaxSize, which Parse refuses.
func (f profileFlags) read() (profile.Files, error) {
	var read profile.Files
	for _, file := range []struct {
		flag, path string
		to         *profile.File
	}{
		{"--p12", f.p12, &read.P12}, {"--sswan", f.sswan, &read.SSwan},
		{"--mobileconfig", f.mobileconfig, &read.Mobileconfig},
	} {
		if file.path == "" {
			continue
		}
		data, err := readCapped(file.path)
		if err != nil {
			return profile.Files{}, fmt.Errorf("cannot read %s: %w", file.flag, err)
		}
		*file.to = profile.File{Name: filepath.Base(file.path), Data: data}
	}
	return read, nil
}

// readCapped reads the file at path up to a byte past profile.MaxSize, so that a stray path, such
// as a device's, reads no more.
func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, profile.MaxSize+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return data, err
}

// flags are install's flags that give the remote network's values.
type flags struct {
	dns, probe      string
	domains, floors []string
}

// settings are the settings f gives, or an error that says what is wrong with them. A domain may
// be written in any case, and with a dot at its end. A floor that routing.Refusal refuses with no
// server's address, one wider than routing.MinBits, is wrong.
func (f flags) settings() (files.Settings, error) {
	if f.dns == "" || len(f.domains) == 0 {
		return files.Settings{}, errors.New("install needs --dns ADDR and --domain NAME")
	}
	var s files.Settings
	var errs []error
	dns, err := netip.ParseAddr(f.dns)
	if err != nil {
		errs = append(errs, fmt.Errorf("--dns %q is no address", f.dns))
	}
	s.DNS = dns.Unmap()
	for _, d := range f.domains {
		s.Domains = append(s.Domains, domainName(d))
	}
	s.Probe = s.Domains[0]
	if f.probe != "" {
		s.Probe = domainName(f.probe)
	}
	for _, floor := range f.floors {
		p, err := netip.ParsePrefix(floor)
		if err != nil {
			errs = append(errs, fmt.Errorf("--floor %q is no range", floor))
			continue
		}
		if why := routing.Refusal(p, nil); why != "" {
			errs = append(errs, fmt.Errorf("--floor %s: %s", p, why))
		}
		s.Floors = append(s.Floors, p)
	}
	if len(errs) == 0 {
		errs = append(errs, s.Check())
	}
	return s, errors.Join(errs...)
}

// clearOfServer refuses, as a mistake on command's line, a floor that holds an address of server,
// the VPN server the profile names. Rule 32001 would take charon's packets there to the floor, and
// the tunnel would never come up to let updown record the address. With no floor, it asks nothing.
func (s system) clearOfServer(command *cobra.Command, server string, floors []netip.Prefix) error {
	if len(floors) == 0 {
		return nil
	}
	addrs, err := s.serverAddrs(command.Context(), server)
	if err != nil {
		return err
	}
	var errs []error
	for _, floor := range floors {
		if why := routing.Refusal(floor, addrs); why != "" {
			errs = append(errs, fmt.Errorf("--floor %s: %s", floor, why))
		}
	}
	if len(errs) > 0 {
		return usageError{errors.Join(errs...), command}
	}
	return nil
}

// serverAddrs are the addresses of server: itself where server is an address, or else those its
// name resolves to now.
func (s system) serverAddrs(ctx context.Context, server string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(server); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	addrs, err := s.names.IPv4(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %s, the VPN server, whose address no --floor may "+
			"hold: %w", server, err)
	}
	return addrs, nil
}

// domainName is name in lower case, without a dot at its end.
func domainName(name string) string {
	return strings.TrimSuffix(strings.ToLower(name), ".")
}
