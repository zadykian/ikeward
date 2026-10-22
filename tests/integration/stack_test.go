package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zadykian/ikeward/internal/profile/profiletest"
)

const (
	// commandTimeout bounds each command the test runs in the client: the watchdog's service,
	// the longest, takes up to its own 2 minutes.
	commandTimeout = 3 * time.Minute
	// upTimeout bounds compose's wait for the services to be healthy, once their images are
	// built.
	upTimeout = "300"
	// cleanupTimeout bounds each of the cleanup's two parts: the logs, and taking the services
	// down. The test's commands end twice that before go test's -timeout, which runs no cleanup.
	cleanupTimeout = 2 * time.Minute
)

// stack is the test's services in Docker Compose, as compose.yaml has them.
type stack struct {
	// env is the environment docker compose runs in, with the paths compose.yaml reads.
	env []string
	// ctx ends the test's commands in time for its cleanup to run: on an interrupt, or ahead of
	// go test's -timeout.
	ctx context.Context
}

// up builds ikeward and writes the test's credentials into a directory of the test's, which
// compose.yaml mounts. It then brings the services up, on fresh volumes, and waits until each is
// healthy. Whatever an interrupted run left goes first. Once the test ends, the services go, with
// their networks, volumes and images; where it failed, their logs go into the test's first.
func up(t *testing.T) *stack {
	t.Helper()
	dir := t.TempDir()
	writeCredentials(t, dir)
	s := &stack{env: append(os.Environ(), "IKEWARD_INTEGRATION_DIR="+dir,
		"IKEWARD_BINARY="+build(t, dir)), ctx: commandContext(t)}
	ctx, cancel := context.WithTimeout(s.ctx, cleanupTimeout)
	defer cancel()
	if _, err := s.compose(ctx, "", "down", "--volumes", "--remove-orphans"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.down(t) })
	t.Cleanup(func() {
		if t.Failed() {
			s.logs(t)
		}
	})
	if _, err := s.compose(s.ctx, "", "up", "--build", "--renew-anon-volumes", "--detach",
		"--wait", "--wait-timeout", upTimeout); err != nil {
		t.Fatal(err)
	}
	return s
}

// commandContext is the test's context, which ends on SIGINT or SIGTERM, and twice
// cleanupTimeout before go test's -timeout. A second SIGINT ends the test at once.
func commandContext(t *testing.T) context.Context {
	t.Helper()
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	t.Cleanup(stop)
	context.AfterFunc(ctx, stop)
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-2*cleanupTimeout))
		t.Cleanup(cancel)
	}
	return ctx
}

// down takes the services down, with their networks, volumes and the images compose built.
func (s *stack) down(t *testing.T) {
	t.Helper()
	// The test's context has ended by the time its cleanup runs.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), cleanupTimeout)
	defer cancel()
	if _, err := s.compose(ctx, "", "down", "--volumes", "--rmi", "local",
		"--remove-orphans"); err != nil {
		t.Error(err)
	}
}

// logs puts in the test's log what the services printed, and the journal of the client's units
// that the test checks.
func (s *stack) logs(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), cleanupTimeout)
	defer cancel()
	for _, args := range [][]string{
		{"logs", "--no-color", "server", "far", "lan"},
		{"exec", "-T", "client", "systemctl", "--failed", "--no-pager"},
		{"exec", "-T", "client", "journalctl", "--boot", "--no-pager",
			"--unit=strongswan.service", "--unit=systemd-networkd.service",
			"--unit=systemd-resolved.service", "--unit=docker.service",
			"--unit=ikeward-watchdog.service", "--unit=ikeward-routing.service"},
	} {
		out, err := s.compose(ctx, "", args...)
		t.Logf("docker compose %s:\n%s", strings.Join(args, " "), out)
		if err != nil {
			t.Log(err)
		}
	}
}

// compose runs docker compose with args, with stdin as its input, and returns what it printed on
// stdout. Where it fails, the error says how, and what it printed.
func (s *stack) compose(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker",
		append([]string{"compose", "--file", "compose.yaml"}, args...)...)
	cmd.Env = s.env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("docker compose %s: %w\n%s%s", strings.Join(args, " "),
			err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

// run runs command in the client, as root, with stdin as its input, and returns what it printed
// on stdout.
func (s *stack) run(t *testing.T, stdin string, command ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, commandTimeout)
	defer cancel()
	return s.compose(ctx, stdin, append([]string{"exec", "-T", "client"}, command...)...)
}

// must runs command in the client as run does, and ends the test where it fails.
func (s *stack) must(t *testing.T, command ...string) string {
	t.Helper()
	out, err := s.run(t, "", command...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// queries is the log of the far host's DNS server, a line for each query it took.
func (s *stack) queries(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, commandTimeout)
	defer cancel()
	out, err := s.compose(ctx, "", "logs", "--no-color", "--no-log-prefix", "far")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// eventually runs check every second until it passes, and ends the test where it still fails
// after timeout, with its last error.
func eventually(t *testing.T, timeout time.Duration, check func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %v: %v", timeout, err)
		}
		time.Sleep(time.Second)
	}
}

// build builds ikeward into dir for Linux on the host's architecture, which Docker runs, and
// returns its path.
func build(t *testing.T, dir string) string {
	t.Helper()
	binary := filepath.Join(dir, "ikeward")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-o", binary,
		"github.com/zadykian/ikeward/cmd/ikeward")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binary
}

// writeCredentials writes into dir the files compose.yaml mounts: the test CA, a certificate from
// it for the VPN server, the server's key, and the client's profile. The profile's certificate is
// from the CA as well, and its .p12 has password.
func writeCredentials(t *testing.T, dir string) {
	t.Helper()
	ca := profiletest.NewCA(t)
	cert, key := ca.Server(t)
	for name, data := range map[string][]byte{
		"ca.pem":          profiletest.PEM(ca.Cert),
		"server-cert.pem": cert,
		"server-key.pem":  key,
		"client.zip":      ca.Zip(t, "client", password),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
