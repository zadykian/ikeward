package host

import (
	"errors"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// Terminal is the terminal that File, standard input, may be.
type Terminal struct{ File *os.File }

// IsTerminal is whether File is a terminal.
func (t Terminal) IsTerminal() bool {
	return term.IsTerminal(int(t.File.Fd()))
}

// ReadPassword reads a line from the terminal, which does not echo it. term.ReadPassword turns
// echo back on only as it returns, which it never does where a signal ends ikeward: Ctrl-C would
// leave the shell's echo off. So an interrupt or SIGTERM as it reads restores the terminal, and
// ends the read with an error that carries up to main.
func (t Terminal) ReadPassword() ([]byte, error) {
	fd := int(t.File.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return readPassword(func() ([]byte, error) { return term.ReadPassword(fd) },
		func() error { return term.Restore(fd, state) }, signals)
}

// readResult is what a read returns.
type readResult struct {
	data []byte
	err  error
}

// readPassword returns what readLine reads, unless a signal comes first: then it has restore
// turn echo back on, and returns an error. readLine's read then stays blocked, until main exits.
func readPassword(readLine func() ([]byte, error), restore func() error,
	signals <-chan os.Signal) ([]byte, error) {
	done := make(chan readResult, 1)
	go func() {
		data, err := readLine()
		done <- readResult{data, err}
	}()
	select {
	case r := <-done:
		return r.data, r.err
	case <-signals:
		if err := restore(); err != nil {
			return nil, errors.Join(errInterrupted, err)
		}
		return nil, errInterrupted
	}
}

// errInterrupted is the error of a read that a signal ended.
var errInterrupted = errors.New("interrupted")
