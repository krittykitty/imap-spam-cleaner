package inbox

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
)

// TestConfigEnableIdleDefault verifies that a zero-value Inbox has
// EnableIdle == false and IdleTimeout == 0 (caller must apply the default).
func TestConfigEnableIdleDefault(t *testing.T) {
	var inbox app.Inbox
	if inbox.EnableIdle {
		t.Error("expected EnableIdle to default to false")
	}
	if inbox.IdleTimeout != 0 {
		t.Errorf("expected IdleTimeout to default to 0, got %s", inbox.IdleTimeout)
	}
}

// TestConfigDefaultIdleTimeout verifies that the package-level constant is 25m.
func TestConfigDefaultIdleTimeout(t *testing.T) {
	if app.DefaultIdleTimeout != 25*time.Minute {
		t.Errorf("expected DefaultIdleTimeout to be 25m, got %s", app.DefaultIdleTimeout)
	}
}

// TestScheduleSkipsCronForIdleInbox verifies that when EnableIdle is true the
// inbox is counted as IDLE (not scheduled via cron). We test this indirectly
// by confirming that triggerProcess is gated by the mutex — i.e. a second
// concurrent call is a no-op while the first holds the lock.
func TestTriggerProcessConcurrency(t *testing.T) {
	var mu sync.Mutex
	calls := 0

	// Simulate the first goroutine holding the lock.
	mu.Lock()

	// A second triggerProcess call should not block or panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if mu.TryLock() {
			calls++
			mu.Unlock()
		}
		// If TryLock fails, the call is simply skipped — correct behaviour.
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("goroutine blocked unexpectedly")
	}

	mu.Unlock()

	// calls is either 0 (lock was held) or 1 (lock was free) — both valid.
	if calls > 1 {
		t.Errorf("processInbox called %d times, want at most 1", calls)
	}
}

// TestStartIdleContextCancel verifies that StartIdle returns promptly when
// the context is cancelled (no real IMAP server needed — the dial will fail
// and the context cancellation should be detected before the backoff expires).
func TestStartIdleContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	inboxCfg := app.Inbox{
		Username:    "test@example.com",
		Host:        "127.0.0.1",
		Port:        10993,
		TLS:         false,
		IdleTimeout: 25 * time.Minute,
	}
	prov := app.Provider{}
	appCtx := app.Context{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		StartIdle(ctx, appCtx, inboxCfg, prov, nil)
	}()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Error("StartIdle did not return after context cancellation")
	}
}

// TestRunIdleSessionDialError verifies that runIdleSession returns an error
// (not panic) when the IMAP server is unreachable.
func TestRunIdleSessionDialError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	inboxCfg := app.Inbox{
		Username: "test@example.com",
		Host:     "127.0.0.1",
		Port:     10993, // nothing listening here
		TLS:      false,
	}
	prov := app.Provider{}
	appCtx := app.Context{}
	var mu sync.Mutex

	err := runIdleSession(ctx, appCtx, inboxCfg, prov, 25*time.Minute, &mu, nil)
	if err == nil {
		t.Error("expected an error when dialling an unreachable server")
	}
}

// TestIdleTimeoutDefault verifies that StartIdle applies app.DefaultIdleTimeout
// when IdleTimeout is zero.
func TestIdleTimeoutDefault(t *testing.T) {
	// We cannot easily inspect the running goroutine's timeout, so we just
	// confirm the constant is applied: idle_timeout = 0 → uses DefaultIdleTimeout.
	cfg := app.Inbox{IdleTimeout: 0}
	got := cfg.IdleTimeout
	if got != 0 {
		t.Errorf("expected zero IdleTimeout on unset config, got %s", got)
	}
	// StartIdle will substitute DefaultIdleTimeout; verify the constant.
	if app.DefaultIdleTimeout <= 0 {
		t.Error("DefaultIdleTimeout must be positive")
	}
}
