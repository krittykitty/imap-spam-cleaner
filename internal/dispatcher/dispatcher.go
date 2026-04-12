// Package dispatcher manages per-provider worker pools that consume analysis
// jobs produced by IMAP IDLE inbox goroutines.
//
// Each configured provider gets its own bounded channel and worker pool.
// Workers apply an optional token-bucket rate limiter and retry failed jobs
// with exponential back-off before reporting a final Result back to the
// caller via job.ResultCh.
package dispatcher

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
	"golang.org/x/time/rate"
)

const (
	defaultConcurrency = 1
	jobChannelBuffer   = 256

	// Retry back-off: base * 2^retries, capped at maxBackoff.
	retryBaseBackoff = 5 * time.Second
	maxBackoff       = 5 * time.Minute
)

// Counters holds observable metrics for a Dispatcher instance.
// All fields are updated atomically and may be read from any goroutine.
type Counters struct {
	Enqueued      atomic.Int64
	Succeeded     atomic.Int64
	Failed        atomic.Int64
	Retried       atomic.Int64
	RateLimitWait atomic.Int64
}

// Dispatcher routes Jobs to per-provider worker pools.
type Dispatcher struct {
	queues   map[string]chan Job
	wg       sync.WaitGroup
	Counters Counters
}

// New creates a Dispatcher and starts worker goroutines for every provider in
// the given map. Workers run until the supplied context is cancelled or
// Shutdown is called (whichever comes first).
func New(ctx context.Context, providers map[string]app.Provider) *Dispatcher {
	d := &Dispatcher{
		queues: make(map[string]chan Job, len(providers)),
	}

	for name, prov := range providers {
		ch := make(chan Job, jobChannelBuffer)
		d.queues[name] = ch

		concurrency := prov.Concurrency
		if concurrency <= 0 {
			concurrency = defaultConcurrency
		}

		var limiter *rate.Limiter
		if prov.RateLimit > 0 {
			limiter = rate.NewLimiter(rate.Limit(prov.RateLimit), 1)
		}

		logx.Infof("[dispatcher] starting %d worker(s) for provider %q (rate_limit=%.2f/s)",
			concurrency, name, prov.RateLimit)

		for w := 0; w < concurrency; w++ {
			d.wg.Add(1)
			go d.runWorker(ctx, name, prov, ch, limiter)
		}
	}

	return d
}

// Submit enqueues a job for the provider named by job.ProviderName.
// If the provider's channel is full, Submit blocks to apply backpressure.
// If the provider is unknown, a failure Result is sent immediately.
func (d *Dispatcher) Submit(job Job) {
	ch, ok := d.queues[job.ProviderName]
	if !ok {
		logx.Errorf("[dispatcher] unknown provider %q for message UID=%d — dropping job",
			job.ProviderName, job.Message.UID)
		sendResult(job.ResultCh, Result{
			UID: job.Message.UID,
			Err: fmt.Errorf("unknown provider %q", job.ProviderName),
		})
		return
	}
	if job.EnqueuedAt.IsZero() {
		job.EnqueuedAt = time.Now()
	}
	d.Counters.Enqueued.Add(1)
	ch <- job
}

// Shutdown stops accepting new jobs, waits for all in-flight jobs to finish,
// and logs final counters. Call this after cancelling the context passed to New.
func (d *Dispatcher) Shutdown() {
	for _, ch := range d.queues {
		close(ch)
	}
	d.wg.Wait()
	logx.Infof("[dispatcher] shutdown complete — enqueued=%d succeeded=%d failed=%d retried=%d rate_waits=%d",
		d.Counters.Enqueued.Load(),
		d.Counters.Succeeded.Load(),
		d.Counters.Failed.Load(),
		d.Counters.Retried.Load(),
		d.Counters.RateLimitWait.Load(),
	)
}

// runWorker is the main loop executed by each provider worker goroutine.
func (d *Dispatcher) runWorker(ctx context.Context, providerName string, provCfg app.Provider, ch <-chan Job, limiter *rate.Limiter) {
	defer d.wg.Done()

	p, err := provider.New(provCfg.Type)
	if err != nil {
		logx.Errorf("[dispatcher] worker for provider %q: failed to create provider: %v", providerName, err)
		// Drain the channel so callers don't block.
		for job := range ch {
			sendResult(job.ResultCh, Result{UID: job.Message.UID, Err: err})
		}
		return
	}
	if err = p.Init(provCfg.Config); err != nil {
		logx.Errorf("[dispatcher] worker for provider %q: failed to init provider: %v", providerName, err)
		for job := range ch {
			sendResult(job.ResultCh, Result{UID: job.Message.UID, Err: err})
		}
		return
	}

	for {
		select {
		case <-ctx.Done():
			// Drain remaining jobs as failures so callers unblock.
			for {
				select {
				case job, ok := <-ch:
					if !ok {
						return
					}
					sendResult(job.ResultCh, Result{UID: job.Message.UID, Err: ctx.Err()})
				default:
					return
				}
			}
		case job, ok := <-ch:
			if !ok {
				return
			}
			d.processJob(ctx, providerName, p, limiter, job)
		}
	}
}

// processJob runs Analyze for one job, applying rate limiting and retry logic.
func (d *Dispatcher) processJob(ctx context.Context, providerName string, p provider.Provider, limiter *rate.Limiter, job Job) {
	tag := fmt.Sprintf("[%s %s %s UID=%d]", job.InboxCfg.Host, job.InboxCfg.Username, job.InboxCfg.Inbox, job.Message.UID)

	if limiter != nil {
		d.Counters.RateLimitWait.Add(1)
		if err := limiter.Wait(ctx); err != nil {
			// Context cancelled while waiting for rate limit token.
			logx.Debugf("[dispatcher] %s rate limiter cancelled: %v", tag, err)
			sendResult(job.ResultCh, Result{UID: job.Message.UID, Err: err})
			return
		}
		d.Counters.RateLimitWait.Add(-1)
	}

	score, err := p.Analyze(job.Message)
	if err != nil {
		maxRetries := job.InboxCfg.MaxRetries
		if maxRetries <= 0 {
			maxRetries = 3
		}
		if job.Retries < maxRetries {
			shift := job.Retries
			if shift > 30 {
				shift = 30
			}
			backoff := retryBaseBackoff * (1 << shift)
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			logx.Warnf("[dispatcher] %s analyze error (attempt %d/%d), retrying in %s: %v",
				tag, job.Retries+1, maxRetries, backoff, err)
			d.Counters.Retried.Add(1)
			job.Retries++
			// Re-enqueue after back-off (on a separate goroutine to avoid blocking the worker).
			ch := d.queues[providerName]
			go func() {
				select {
				case <-time.After(backoff):
					ch <- job
				case <-ctx.Done():
					sendResult(job.ResultCh, Result{UID: job.Message.UID, Err: ctx.Err()})
				}
			}()
			return
		}
		logx.Errorf("[dispatcher] %s analyze failed after %d attempts: %v", tag, maxRetries, err)
		d.Counters.Failed.Add(1)
		sendResult(job.ResultCh, Result{UID: job.Message.UID, Err: err})
		return
	}

	logx.Debugf("[dispatcher] %s spam score: %d/100 (provider=%s)", tag, score, providerName)
	d.Counters.Succeeded.Add(1)
	sendResult(job.ResultCh, Result{
		UID:        job.Message.UID,
		Success:    true,
		SpamScore:  score,
		ShouldMove: score >= job.InboxCfg.MinScore,
	})
}

// sendResult delivers r to ch without blocking. If ch is nil the result is discarded.
func sendResult(ch chan<- Result, r Result) {
	if ch == nil {
		return
	}
	ch <- r
}
