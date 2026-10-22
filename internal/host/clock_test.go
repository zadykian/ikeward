package host

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Sleep waits its time, or returns as soon as its context ends, with the context's error.
func TestClock(t *testing.T) {
	before := time.Now()
	if err := (Clock{}).Sleep(t.Context(), time.Millisecond); err != nil {
		t.Errorf("Sleep: %v", err)
	}
	if now := (Clock{}).Now(); now.Sub(before) < time.Millisecond {
		t.Errorf("Now is %v, under a millisecond after %v and a sleep of one", now, before)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (Clock{}).Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("Sleep with its context ended: %v, want %v", err, context.Canceled)
	}
}
