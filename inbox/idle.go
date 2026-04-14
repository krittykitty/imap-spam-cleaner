package inbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/internal/dispatcher"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/emersion/go-imap/v2/imapclient"
)

const (
	idleBackoffMin = 5 * time.Second
	idleBackoffMax = 5 * time.Minute
	idleBackoffMul = 2
)

// StartIdle runs a blocking IMAP IDLE loop for a single inbox until ctx is
// cancelled. It performs an initial catch-up run on startup and re-triggers
// processInbox whenever the server signals new mail. IDLE is re-issued every
// idle_timeout to comply with RFC 2177. Reconnection uses exponential backoff.
func StartIdle(ctx context.Context, appCtx app.Context, inboxCfg app.Inbox, prov app.Provider, disp *dispatcher.Dispatcher) {
	idleTimeout := inboxCfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = app.DefaultIdleTimeout
	}

	logx.Infof("IDLE watcher started for %s (timeout=%s)", inboxCfg.Username, idleTimeout)

	var mu sync.Mutex // prevents concurrent processInbox calls for this inbox

	backoff := idleBackoffMin

	for {
		if ctx.Err() != nil {
			logx.Infof("IDLE watcher stopping for %s", inboxCfg.Username)
			return
		}

		err := runIdleSession(ctx, appCtx, inboxCfg, prov, idleTimeout, &mu, disp)
		if ctx.Err() != nil {
			logx.Infof("IDLE watcher stopping for %s", inboxCfg.Username)
			return
		}

		logx.Errorf("IDLE session error for %s: %v; reconnecting in %s", inboxCfg.Username, err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff = minDuration(backoff*idleBackoffMul, idleBackoffMax)
	}
}

// runIdleSession opens one IMAP connection, performs a catch-up run, then
// enters IDLE until ctx is cancelled or an error occurs. The returned error is
// always non-nil; callers should check ctx.Err() to distinguish shutdown from
// transient failure.
func runIdleSession(
	ctx context.Context,
	appCtx app.Context,
	inboxCfg app.Inbox,
	prov app.Provider,
	idleTimeout time.Duration,
	mu *sync.Mutex,
	disp *dispatcher.Dispatcher,
) error {
	// Channel used by the unilateral-data handler to signal new mail.
	newMail := make(chan struct{}, 1)

	opts := &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					// Non-blocking send — if a signal is already pending we
					// don't need to queue another one.
					select {
					case newMail <- struct{}{}:
					default:
					}
				}
			},
		},
	}

	var (
		c   *imapclient.Client
		err error
	)
	addr := fmt.Sprintf("%s:%d", inboxCfg.Host, inboxCfg.Port)
	if inboxCfg.TLS {
		c, err = imapclient.DialTLS(addr, opts)
	} else {
		c, err = imapclient.DialInsecure(addr, opts)
	}
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() {
		_ = c.Logout().Wait()
		_ = c.Close()
	}()

	if err = c.Login(inboxCfg.Username, inboxCfg.Password).Wait(); err != nil {
		return fmt.Errorf("login: %w", err)
	}

	if _, err = c.Select(inboxCfg.Inbox, nil).Wait(); err != nil {
		return fmt.Errorf("select: %w", err)
	}

	logx.Debugf("IDLE session connected for %s", inboxCfg.Username)

	// Catch-up: process any messages that arrived since the last checkpoint.
	triggerProcess(ctx, appCtx, inboxCfg, prov, mu, disp)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		idleCmd, idleErr := c.Idle()
		if idleErr != nil {
			return fmt.Errorf("IDLE command: %w", idleErr)
		}

		timer := time.NewTimer(idleTimeout)
		triggered := false

		select {
		case <-ctx.Done():
			timer.Stop()
			_ = idleCmd.Close()
			_ = idleCmd.Wait()
			return ctx.Err()

		case <-timer.C:
			// Re-issue IDLE (RFC 2177 compliance): close the current command and
			// let the loop restart a fresh IDLE in the next iteration.

		case <-newMail:
			triggered = true
		}

		timer.Stop()
		if closeErr := idleCmd.Close(); closeErr != nil {
			return fmt.Errorf("close IDLE: %w", closeErr)
		}
		if waitErr := idleCmd.Wait(); waitErr != nil {
			return fmt.Errorf("wait IDLE: %w", waitErr)
		}

		if triggered {
			logx.Debugf("IDLE: new mail notification for %s", inboxCfg.Username)
			triggerProcess(ctx, appCtx, inboxCfg, prov, mu, disp)
		}
	}
}

// triggerProcess runs processInbox in a goroutine, guarded by mu to ensure
// only one concurrent run per inbox.
func triggerProcess(runCtx context.Context, appCtx app.Context, inboxCfg app.Inbox, prov app.Provider, mu *sync.Mutex, disp *dispatcher.Dispatcher) {
	go func() {
		if !mu.TryLock() {
			logx.Debugf("IDLE: processInbox already running for %s, skipping trigger", inboxCfg.Username)
			return
		}
		defer mu.Unlock()
		if disp == nil {
			processInbox(appCtx, inboxCfg, prov)
		} else {
			processInboxInternal(appCtx, inboxCfg, prov, disp, runCtx)
		}
	}()
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
