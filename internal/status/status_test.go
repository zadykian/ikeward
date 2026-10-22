package status

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
)

// Every check passes on a live server with the VPN up, and each asks for the VPN's connection,
// link, table and resolver, strongSwan and the resolver with a timeout.
func TestHealthy(t *testing.T) {
	f := healthy()
	results := f.system().Check(t.Context())
	if got := lines(results); !slices.Equal(got, capturedLines) {
		t.Errorf("printed:\n%s\nwant:\n%s", strings.Join(got, "\n"),
			strings.Join(capturedLines, "\n"))
	}
	if got := Failed(results); got != 0 {
		t.Errorf("%d checks failed", got)
	}
	if f.tunnel.asked != "ikeward" || !f.tunnel.deadline {
		t.Errorf("asked strongSwan for %q, with a deadline: %v", f.tunnel.asked, f.tunnel.deadline)
	}
	want := []string{"192.0.2.10:53 example.test"}
	if !slices.Equal(f.resolver.asked, want) || !f.resolver.deadline {
		t.Errorf("asked the resolver %q, with a deadline: %v, want %q", f.resolver.asked,
			f.resolver.deadline, want)
	}
	want = []string{"resolvectl --json=short status ikeward0"}
	if !slices.Equal(f.runner.commands, want) {
		t.Errorf("ran %q, want %q", f.runner.commands, want)
	}
}

// Where a check of the tunnel fails, the resolver is not asked, as the query would not go
// through the tunnel.
func TestResolverNotAsked(t *testing.T) {
	f := healthy()
	f.network.rules = nil
	results := f.system().Check(t.Context())
	if len(f.resolver.asked) > 0 {
		t.Errorf("asked the resolver %q", f.resolver.asked)
	}
	if got, want := results[5].String(),
		"FAIL  resolver  not asked, as the tunnel's checks failed"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

// The resolver check keeps Lookup's error, whether the resolver answers or not, so that the
// watchdog tells the two apart. The other checks keep none.
func TestResolverError(t *testing.T) {
	answer := fmt.Errorf("%w SERVFAIL", ErrAnswer)
	for _, err := range []error{answer, errors.New("no answer in time")} {
		f := healthy()
		f.resolver.err = err
		for _, r := range f.system().Check(t.Context()) {
			var want error
			if r.Name == "resolver" {
				want = err
			}
			if !errors.Is(r.Err, want) {
				t.Errorf("%s: Err is %v, want %v", r.Name, r.Err, want)
			}
		}
	}
}

// The resolver check asks the settings' resolver for their probe.
func TestResolverSettings(t *testing.T) {
	f := healthy()
	f.files.files[files.SettingsFile] = "dns 10.1.2.3\ndomain example.test\n" +
		"probe host.example.test\n"
	r := f.system().Check(t.Context())[5]
	want := []string{"10.1.2.3:53 host.example.test"}
	if !slices.Equal(f.resolver.asked, want) || r.String() !=
		"ok    resolver  10.1.2.3 answers for host.example.test through the tunnel" {
		t.Errorf("asked %q, want %q: %s", f.resolver.asked, want, r)
	}
}

// Without the settings, the resolver, DNS and floor checks fail with routing.ReadSettings' error,
// which each keeps. Neither the resolver nor resolvectl is asked, and the other checks pass.
func TestSettingsUnread(t *testing.T) {
	for name, data := range map[string]string{"missing": "", "wrong": "dns 192.0.2.10\n"} {
		f := healthy()
		delete(f.files.files, files.SettingsFile)
		if data != "" {
			f.files.files[files.SettingsFile] = data
		}
		_, readErr := routing.ReadSettings(f.files)
		results := f.system().Check(t.Context())
		for i, r := range results {
			unread := i >= 5 && i <= 7
			if r.OK == unread || unread && (r.Detail != readErr.Error() ||
				!errors.Is(r.Err, routing.ErrSettings)) || !unread && r.Err != nil {
				t.Errorf("%s: %s, keeping %v", name, r, r.Err)
			}
		}
		if len(f.resolver.asked) > 0 || len(f.runner.commands) > 0 {
			t.Errorf("%s: asked the resolver %q, and ran %q", name, f.resolver.asked,
				f.runner.commands)
		}
	}
}
