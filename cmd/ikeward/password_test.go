package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// On a terminal, install asks for the password, which the terminal does not echo; a .p12 without
// a password asks for none.
func TestInstallAsks(t *testing.T) {
	f := newFakeSystem(t, "not this\n")
	f.term = fakeTerminal{terminal: true, password: "secret"}
	if err := run(installArgs(writeProfile(t, "secret")), f.system); err != nil {
		t.Fatal(err)
	}
	if got, want := f.errOut.String(), "Password of the profile's .p12: \n"; got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
	if f.term.reads != 1 {
		t.Errorf("read the terminal %d times, want once", f.term.reads)
	}

	f = newFakeSystem(t, "")
	f.term = fakeTerminal{terminal: true}
	if err := run(installArgs(writeProfile(t, "")), f.system); err != nil {
		t.Fatal(err)
	}
	if f.term.reads > 0 || f.errOut.Len() > 0 {
		t.Errorf("read the terminal %d times, and printed %q", f.term.reads, f.errOut.String())
	}
}

func TestFirstLine(t *testing.T) {
	long := strings.Repeat("x", maxPassword)
	for _, test := range []struct {
		name string
		in   io.Reader
		want string
		err  string
	}{
		{"line", strings.NewReader("pass word\nmore\n"), "pass word", ""},
		{"CRLF", strings.NewReader("secret\r\n"), "secret", ""},
		{"no newline", strings.NewReader("secret"), "secret", ""},
		{"empty line", strings.NewReader("\n"), "", ""},
		{"longest", strings.NewReader(long[1:] + "\n"), long[1:], ""},
		{"empty", strings.NewReader(""), "", "standard input, not a terminal, is empty"},
		{"too long", strings.NewReader(long + "\n"), "",
			"standard input's first line is over 4096 bytes"},
		{"failing", iotest.ErrReader(errors.New("broken")), "",
			"cannot read standard input: broken"},
	} {
		got, err := firstLine(test.in)
		if got != test.want || (err == nil) != (test.err == "") ||
			(err != nil && err.Error() != test.err) {
			t.Errorf("%s: %q, %v; want %q, %q", test.name, got, err, test.want, test.err)
		}
	}
}
