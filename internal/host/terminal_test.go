package host

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

// A read that ends returns what it read, and leaves the terminal be.
func TestReadPassword(t *testing.T) {
	restored := 0
	got, err := readPassword(func() ([]byte, error) { return []byte("secret"), nil },
		func() error { restored++; return nil }, make(chan os.Signal))
	if string(got) != "secret" || err != nil || restored > 0 {
		t.Errorf("readPassword: %q, %v, restored %d times; want secret", got, err, restored)
	}
	failed := errors.New("failed")
	if _, err := readPassword(func() ([]byte, error) { return nil, failed },
		func() error { return nil }, make(chan os.Signal)); !errors.Is(err, failed) {
		t.Errorf("readPassword: %v, want the read's error", err)
	}
}

// A signal as it reads restores the terminal, and ends the read with an error.
func TestReadPasswordInterrupted(t *testing.T) {
	for _, test := range []struct {
		restoreErr error
		want       string
	}{
		{nil, "interrupted"},
		{errors.New("bad file descriptor"), "interrupted\nbad file descriptor"},
	} {
		blocked := make(chan struct{})
		defer close(blocked)
		signals := make(chan os.Signal, 1)
		signals <- syscall.SIGINT
		restored := 0
		got, err := readPassword(func() ([]byte, error) { <-blocked; return nil, nil },
			func() error { restored++; return test.restoreErr }, signals)
		if got != nil || err == nil || err.Error() != test.want || restored != 1 {
			t.Errorf("readPassword: %q, %v, restored %d times; want %q, restored once", got, err,
				restored, test.want)
		}
	}
}

// A file is no terminal, and a terminal's password cannot be read from it.
func TestTerminal(t *testing.T) {
	f, err := os.Create(t.TempDir() + "/file")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // a test's file, written by no one
	if (Terminal{File: f}).IsTerminal() {
		t.Error("a file is a terminal")
	}
	if _, err := (Terminal{File: f}).ReadPassword(); err == nil {
		t.Error("ReadPassword read from a file")
	}
}
