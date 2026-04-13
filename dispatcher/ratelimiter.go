package dispatcher

import (
	"context"
	"time"
)

// rateLimiter implements a simple token-bucket rate limiter backed by a
// time.Ticker. Exactly one token is produced every 1/rps seconds; if the
// bucket is already full the surplus token is dropped, so bursting is not
// supported — callers are gated to at most rps calls per second.
type rateLimiter struct {
	ch   chan struct{}
	stop chan struct{}
}

func newRateLimiter(rps float64) *rateLimiter {
	interval := time.Duration(float64(time.Second) / rps)
	rl := &rateLimiter{
		ch:   make(chan struct{}, 1),
		stop: make(chan struct{}),
	}
	go rl.run(interval)
	return rl
}

func (r *rateLimiter) run(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			select {
			case r.ch <- struct{}{}:
			default: // bucket full — drop
			}
		case <-r.stop:
			return
		}
	}
}

// Wait blocks until a token is available, ctx is cancelled, or the limiter is
// closed.
func (r *rateLimiter) Wait(ctx context.Context) error {
	select {
	case <-r.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.stop:
		return context.Canceled
	}
}

// Close shuts down the token-generation goroutine. Safe to call multiple times.
func (r *rateLimiter) Close() {
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
}
