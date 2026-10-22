package host

import (
	"context"
	"time"
)

// Clock is the system's clock.
type Clock struct{}

// Now is the time now.
func (Clock) Now() time.Time { return time.Now() }

// Sleep waits for d, or until ctx ends, and returns ctx's error where it ends first.
func (Clock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
