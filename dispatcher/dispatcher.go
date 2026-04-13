// Package dispatcher provides a bounded per-provider worker pool with optional
// token-bucket rate limiting and exponential-backoff retry logic for spam
// analysis jobs.
//
// A Dispatcher is created once per provider and shared across all IDLE inboxes
// that use that provider. It serialises API access to at most [concurrency]
// parallel calls and, when rate_limit > 0, to at most rate_limit calls per
// second.
package dispatcher

import (
	"context"
	"fmt"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
)

const (
	retryBackoffMin = 1 * time.Second
	retryBackoffMax = 5 * time.Minute
	retryBackoffMul = 2
)

type job struct {
	ctx        context.Context //nolint:containedctx
	msg        imap.Message
	maxRetries int
	result     chan jobResult
}

type jobResult struct {
	score int
	err   error
}

// Dispatcher manages a bounded pool of provider workers with optional
// rate-limiting and exponential-backoff retry.
type Dispatcher struct {
	jobs chan job
	done chan struct{}
	rl   *rateLimiter
}

// New creates a Dispatcher for the given provider type and config.
//
//   - concurrency — worker-pool size (clamped to ≥ 1).
//   - rateLimit   — maximum calls per second shared across all workers; 0
//     means no limit.
//
// Each worker owns an independently initialised Provider instance so that
// concurrent calls are safe.
func New(provType string, provCfg map[string]string, concurrency int, rateLimit float64) (*Dispatcher, error) {
	if concurrency < 1 {
		concurrency = 1
	}

	var rl *rateLimiter
	if rateLimit > 0 {
		rl = newRateLimiter(rateLimit)
	}

	d := &Dispatcher{
		jobs: make(chan job, concurrency*2),
		done: make(chan struct{}),
		rl:   rl,
	}

	for i := 0; i < concurrency; i++ {
		p, err := provider.New(provType)
		if err != nil {
			d.Stop()
			return nil, fmt.Errorf("dispatcher: failed to create provider: %w", err)
		}
		if err = p.Init(provCfg); err != nil {
			d.Stop()
			return nil, fmt.Errorf("dispatcher: failed to init provider: %w", err)
		}
		go d.runWorker(p)
	}

	return d, nil
}

// Analyze submits a message for spam analysis. It blocks until a worker
// accepts the job, the analysis completes (with up to maxRetries retries), or
// ctx is cancelled.
func (d *Dispatcher) Analyze(ctx context.Context, msg imap.Message, maxRetries int) (int, error) {
	res := make(chan jobResult, 1)
	j := job{ctx: ctx, msg: msg, maxRetries: maxRetries, result: res}

	select {
	case d.jobs <- j:
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-d.done:
		return 0, fmt.Errorf("dispatcher: stopped")
	}

	select {
	case r := <-res:
		return r.score, r.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Stop shuts down all workers and the rate-limiter goroutine. It is safe to
// call Stop multiple times.
func (d *Dispatcher) Stop() {
	select {
	case <-d.done:
	default:
		close(d.done)
	}
	if d.rl != nil {
		d.rl.Close()
	}
}

func (d *Dispatcher) runWorker(p provider.Provider) {
	for {
		select {
		case <-d.done:
			return
		case j, ok := <-d.jobs:
			if !ok {
				return
			}
			d.processJob(p, j)
		}
	}
}

func (d *Dispatcher) processJob(p provider.Provider, j job) {
	// Gate on the rate limiter before the first attempt.
	if d.rl != nil {
		if err := d.rl.Wait(j.ctx); err != nil {
			j.result <- jobResult{err: err}
			return
		}
	}

	maxRetries := j.maxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	backoff := retryBackoffMin

	var (
		score int
		err   error
	)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-j.ctx.Done():
				j.result <- jobResult{err: j.ctx.Err()}
				return
			case <-d.done:
				j.result <- jobResult{err: fmt.Errorf("dispatcher: stopped during retry")}
				return
			case <-time.After(backoff):
			}
			backoff = minDuration(backoff*retryBackoffMul, retryBackoffMax)
		}

		score, err = p.Analyze(j.msg)
		if err == nil {
			break
		}
		logx.Warnf("dispatcher: analysis failed for message #%d (attempt %d/%d): %v",
			j.msg.UID, attempt+1, maxRetries+1, err)
	}

	j.result <- jobResult{score: score, err: err}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
