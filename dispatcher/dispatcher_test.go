package dispatcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
)

// TestMinDuration verifies the helper returns the smaller of two durations.
func TestMinDuration(t *testing.T) {
	if got := minDuration(2*time.Second, 5*time.Second); got != 2*time.Second {
		t.Errorf("expected 2s, got %s", got)
	}
	if got := minDuration(5*time.Second, 2*time.Second); got != 2*time.Second {
		t.Errorf("expected 2s, got %s", got)
	}
}

// TestRateLimiterWait verifies that the rate limiter grants tokens at
// approximately the configured rate.
func TestRateLimiterWait(t *testing.T) {
	rl := newRateLimiter(10) // 10 tokens/s → one every 100 ms
	defer rl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := rl.Wait(ctx); err != nil {
			t.Fatalf("Wait failed: %v", err)
		}
	}
	// Three tokens at 10/s should take roughly 200 ms (first token may be
	// immediate if the ticker fires immediately).
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Errorf("rate limiter too slow: took %s for 3 tokens at 10/s", elapsed)
	}
}

// TestRateLimiterContextCancel verifies that Wait returns the context error
// when ctx is cancelled.
func TestRateLimiterContextCancel(t *testing.T) {
	rl := newRateLimiter(0.001) // extremely slow — token never arrives in time
	defer rl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := rl.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

// TestRateLimiterClose verifies that Close is idempotent and does not panic.
func TestRateLimiterClose(t *testing.T) {
	rl := newRateLimiter(10)
	rl.Close()
	rl.Close() // second call must not panic
}

// TestDispatcherUnknownProvider verifies that New returns an error for an
// unknown provider type.
func TestDispatcherUnknownProvider(t *testing.T) {
	_, err := New("nonexistent-provider", map[string]string{}, 1, 0)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

// TestDispatcherStop verifies that Stop is idempotent and does not panic.
func TestDispatcherStop(t *testing.T) {
	d := &Dispatcher{
		jobs: make(chan job, 1),
		done: make(chan struct{}),
	}
	d.Stop()
	d.Stop() // second call must not panic
}

// TestDispatcherAnalyzeStoppedDispatcher verifies that Analyze returns an
// error immediately when the dispatcher has been stopped.
func TestDispatcherAnalyzeStoppedDispatcher(t *testing.T) {
	d := &Dispatcher{
		jobs: make(chan job, 1),
		done: make(chan struct{}),
	}
	d.Stop() // mark as stopped before any call

	_, err := d.Analyze(context.Background(), imap.Message{}, 0)
	if err == nil {
		t.Fatal("expected error from stopped dispatcher")
	}
}

// TestDispatcherAnalyzeContextCancel verifies that Analyze respects context
// cancellation when the worker queue is full.
func TestDispatcherAnalyzeContextCancel(t *testing.T) {
	// A dispatcher with no workers and a full queue will block forever on Submit.
	d := &Dispatcher{
		jobs: make(chan job, 0), // unbuffered — blocked immediately
		done: make(chan struct{}),
	}
	defer d.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := d.Analyze(ctx, imap.Message{}, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

// TestProcessJobRetry verifies that processJob retries analysis up to
// maxRetries times and returns the last error on exhaustion.
func TestProcessJobRetry(t *testing.T) {
	calls := 0
	p := &fakeProvider{
		analyze: func() (int, error) {
			calls++
			return 0, errors.New("transient error")
		},
	}

	d := &Dispatcher{
		jobs: make(chan job, 1),
		done: make(chan struct{}),
	}

	res := make(chan jobResult, 1)
	j := job{
		ctx:        context.Background(),
		msg:        imap.Message{},
		maxRetries: 2,
		result:     res,
	}

	d.processJob(p, j)

	r := <-res
	if r.err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

// TestProcessJobSuccess verifies that processJob sends the correct score on
// success.
func TestProcessJobSuccess(t *testing.T) {
	p := &fakeProvider{
		analyze: func() (int, error) {
			return 75, nil
		},
	}

	d := &Dispatcher{
		jobs: make(chan job, 1),
		done: make(chan struct{}),
	}

	res := make(chan jobResult, 1)
	j := job{
		ctx:        context.Background(),
		msg:        imap.Message{},
		maxRetries: 0,
		result:     res,
	}

	d.processJob(p, j)

	r := <-res
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}
	if r.score != 75 {
		t.Errorf("expected score 75, got %d", r.score)
	}
}

// TestProcessJobContextCancel verifies that processJob aborts during the
// backoff sleep when ctx is cancelled.
func TestProcessJobContextCancel(t *testing.T) {
	calls := 0
	p := &fakeProvider{
		analyze: func() (int, error) {
			calls++
			return 0, errors.New("transient error")
		},
	}

	d := &Dispatcher{
		jobs: make(chan job, 1),
		done: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())

	res := make(chan jobResult, 1)
	j := job{
		ctx:        ctx,
		msg:        imap.Message{},
		maxRetries: 5,
		result:     res,
	}

	// Cancel after the first attempt completes.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	d.processJob(p, j)

	r := <-res
	if r.err == nil {
		t.Fatal("expected error after context cancel")
	}
	// Should have attempted only once before the cancel fired.
	if calls != 1 {
		t.Errorf("expected 1 call before cancel, got %d", calls)
	}
}

// fakeProvider is a minimal provider.Provider implementation for testing.
type fakeProvider struct {
	analyze func() (int, error)
}

func (f *fakeProvider) Name() string                                  { return "fake" }
func (f *fakeProvider) Init(_ map[string]string) error                { return nil }
func (f *fakeProvider) ValidateConfig(_ map[string]string) error      { return nil }
func (f *fakeProvider) HealthCheck(_ map[string]string) error         { return nil }
func (f *fakeProvider) Analyze(_ imap.Message) (int, error)           { return f.analyze() }
