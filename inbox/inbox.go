package inbox

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/checkpoint"
	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/internal/dispatcher"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
	goimap "github.com/emersion/go-imap/v2"
	"github.com/go-co-op/gocron/v2"
)

const (
	defaultIdleTimeout   = 25 * time.Minute
	maxReconnectBackoff  = 5 * time.Minute
	shutdownDrainTimeout = 30 * time.Second
)

// Schedule runs the cron scheduler for non-IDLE inboxes, spawns IDLE
// goroutines for IDLE-enabled inboxes, and blocks until SIGINT/SIGTERM.
func Schedule(ctx context.Context, appCtx app.Context) {
	disp := dispatcher.New(ctx, appCtx.Config.Providers)

	s, err := gocron.NewScheduler()
	if err != nil {
		logx.Errorf("Could not create scheduler: %v", err)
		return
	}

	idleCount := 0
	for i, inbox := range appCtx.Config.Inboxes {
		prov, ok := appCtx.Config.Providers[inbox.Provider]
		if !ok {
			logx.Errorf("Invalid provider %s for inbox %d", inbox.Provider, i)
			continue
		}

		if inbox.EnableIdle {
			idleCount++
			logx.Infof("Starting IDLE listener for inbox %s (%s)", inbox.Username, inbox.Inbox)
			go runIdleInbox(ctx, appCtx, inbox, prov, disp)
		} else {
			logx.Infof("Scheduling inbox %s (%s)", inbox.Username, inbox.Schedule)
			if _, err = s.NewJob(
				gocron.CronJob(inbox.Schedule, false),
				gocron.NewTask(processInbox, appCtx, inbox, prov),
			); err != nil {
				logx.Errorf("Could not schedule inbox %s (%s): %v", inbox.Username, inbox.Schedule, err)
			}
		}
		jobs++
	}

	logx.Debugf("Scheduled %d inbox jobs", jobs)
	logx.Debugf("Starting scheduler")
	s.Start()
	logx.Debugf("Scheduler started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	sig := <-sigCh
	logx.Debugf("Received %s, shutting down", sig.String())

	if err = s.Shutdown(); err != nil {
		logx.Errorf("Could not shutdown scheduler: %v", err)
	}

	if idleCount > 0 {
		// Intentionally use context.Background() (not the already-cancelled
		// parent ctx) so the drain window is not immediately cancelled.
		// Workers have shutdownDrainTimeout to finish in-flight jobs.
		drainCtx, cancel := context.WithTimeout(context.Background(), shutdownDrainTimeout)
		defer cancel()
		done := make(chan struct{})
		go func() {
			disp.Shutdown()
			close(done)
		}()
		select {
		case <-done:
		case <-drainCtx.Done():
			logx.Warnf("Dispatcher drain timed out after %s", shutdownDrainTimeout)
		}
	}
}

// RunAllInboxes processes every inbox once synchronously (used with -now flag).
func RunAllInboxes(ctx app.Context) {
	for i, inbox := range ctx.Config.Inboxes {
		logx.Infof("Processing inbox %s", inbox.Username)
		prov, ok := ctx.Config.Providers[inbox.Provider]
		if !ok {
			logx.Errorf("Invalid provider %s for inbox %d", inbox.Provider, i)
			continue
		}
		processInbox(ctx, inbox, prov)
	}
}

// runIdleInbox is the long-running goroutine for a single IDLE-enabled inbox.
// It performs an initial catch-up via processInbox, then enters an IDLE loop
// that detects new messages in real time and submits them as dispatcher.Jobs.
func runIdleInbox(ctx context.Context, appCtx app.Context, inboxCfg app.Inbox, prov app.Provider, disp *dispatcher.Dispatcher) {
	tag := logTag(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox)

	// Initial catch-up: process any messages that arrived while we were down.
	logx.Infof("%s initial catch-up run", tag)
	processInbox(appCtx, inboxCfg, prov)

	// Load the checkpoint that processInbox just created/updated.
	cp, err := checkpoint.Load(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox)
	if err != nil || cp == nil {
		logx.Errorf("%s could not load checkpoint after catch-up: %v", tag, err)
		return
	}

	mgr := checkpoint.NewManager(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox, cp)

	// resultCh receives analysis outcomes from dispatcher workers.
	resultCh := make(chan dispatcher.Result, 64)
	pending := make(map[uint32]struct{})

	idleTimeout := inboxCfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}

	reconnectBackoff := time.Second

	// Initial connection.
	var im *imap.Imap
	for {
		if ctx.Err() != nil {
			return
		}
		im, err = imap.NewForIdle(inboxCfg)
		if err == nil {
			break
		}
		logx.Errorf("%s IMAP connect failed: %v — retrying in %s", tag, err, reconnectBackoff)
		select {
		case <-time.After(reconnectBackoff):
		case <-ctx.Done():
			return
		}
		if reconnectBackoff < maxReconnectBackoff {
			reconnectBackoff *= 2
		}
	}
	reconnectBackoff = time.Second
	defer im.Close()

	for {
		if ctx.Err() != nil {
			// Drain any pending results before exiting.
			drainResults(mgr, resultCh, inboxCfg, im, appCtx, tag, pending)
			return
		}

		// Drain any completed results before checking for new UIDs.
		drainResults(mgr, resultCh, inboxCfg, im, appCtx, tag, pending)

		// Search for new UIDs since the last checkpoint.
		newUIDs, err := im.SearchNewUIDs(goimap.UID(mgr.LastUID()))
		if err != nil {
			logx.Errorf("%s SearchNewUIDs error: %v — reconnecting", tag, err)
			im.Close()
			im, reconnectBackoff = reconnect(ctx, inboxCfg, reconnectBackoff, tag)
			if im == nil {
				return
			}
			continue
		}
		reconnectBackoff = time.Second

		if len(newUIDs) > 0 {
			logx.Infof("%s found %d new UID(s) since UID %d", tag, len(newUIDs), mgr.LastUID())
			enqueueUIDs(im, newUIDs, inboxCfg, prov, disp, resultCh, appCtx, tag, pending)
		}

		idleCtx, cancel := context.WithCancel(ctx)
		idleErrCh := make(chan error, 1)
		go func() {
			idleErrCh <- im.IdleUntilNew(idleCtx, idleTimeout)
		}()

		select {
		case res := <-resultCh:
			cancel()
			if _, ok := pending[uint32(res.UID)]; ok {
				delete(pending, uint32(res.UID))
			}
			if err := <-idleErrCh; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logx.Errorf("%s IDLE stop error: %v", tag, err)
			}
			handleResult(res, mgr, inboxCfg, im, appCtx, tag)
		case err := <-idleErrCh:
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			logx.Errorf("%s IDLE error: %v — reconnecting", tag, err)
			im.Close()
			im, reconnectBackoff = reconnect(ctx, inboxCfg, reconnectBackoff, tag)
			if im == nil {
				return
			}
		case <-ctx.Done():
			cancel()
			if err := <-idleErrCh; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logx.Errorf("%s IDLE stop error: %v", tag, err)
			}
			drainResults(mgr, resultCh, inboxCfg, im, appCtx, tag, pending)
			return
		}
	}
}

// enqueueUIDs fetches messages for the given UIDs and submits each as a Job.
func enqueueUIDs(
	im *imap.Imap,
	uids []goimap.UID,
	inboxCfg app.Inbox,
	prov app.Provider,
	disp *dispatcher.Dispatcher,
	resultCh chan dispatcher.Result,
	appCtx app.Context,
	tag string,
	pending map[uint32]struct{},
) {
	if len(uids) == 0 {
		return
	}

	var uidSet goimap.UIDSet
	for _, uid := range uids {
		uidSet.AddNum(uid)
	}

	msgs, err := im.LoadMessagesByUID(uidSet)
	if err != nil {
		logx.Errorf("%s failed to fetch messages: %v", tag, err)
		return
	}

	for _, msg := range msgs {
		if _, ok := pending[uint32(msg.UID)]; ok {
			continue
		}
		if wl, ok := appCtx.Config.Whitelists[inboxCfg.Whitelist]; ok {
			if isTrustedSender(msg.From, wl) {
				logx.Debugf("%s UID=%d skipping trusted sender (%s)", tag, msg.UID, msg.From)
				// Send a synthetic success result so the checkpoint advances.
				// The channel is buffered; use a blocking send to guarantee
				// delivery — dropping this result would cause the UID to be
				// reprocessed on the next run.
				pending[uint32(msg.UID)] = struct{}{}
				resultCh <- dispatcher.Result{UID: msg.UID, Success: true}
				continue
			}
		}

		job := dispatcher.Job{
			InboxCfg:     inboxCfg,
			ProviderCfg:  prov,
			ProviderName: inboxCfg.Provider,
			Message:      msg,
			EnqueuedAt:   time.Now(),
			ResultCh:     resultCh,
		}
		pending[uint32(msg.UID)] = struct{}{}
		logx.Debugf("%s UID=%d submitting to dispatcher (provider=%s)", tag, msg.UID, inboxCfg.Provider)
		disp.Submit(job)
	}
}

// drainResults reads all pending results from resultCh without blocking.
func drainResults(
	mgr *checkpoint.Manager,
	resultCh <-chan dispatcher.Result,
	inboxCfg app.Inbox,
	im *imap.Imap,
	appCtx app.Context,
	tag string,
	pending map[uint32]struct{},
) {
	for {
		select {
		case res := <-resultCh:
			if _, ok := pending[uint32(res.UID)]; ok {
				delete(pending, uint32(res.UID))
			}
			handleResult(res, mgr, inboxCfg, im, appCtx, tag)
		default:
			return
		}
	}
}

// handleResult processes a single dispatcher.Result: moves the message if
// needed and advances the checkpoint.
func handleResult(
	res dispatcher.Result,
	mgr *checkpoint.Manager,
	inboxCfg app.Inbox,
	im *imap.Imap,
	appCtx app.Context,
	tag string,
) {
	if !res.Success {
		logx.Errorf("%s UID=%d analysis failed: %v — not advancing checkpoint", tag, res.UID, res.Err)
		return
	}

	if res.ShouldMove {
		if appCtx.Options.AnalyzeOnly {
			logx.Debugf("%s UID=%d analyze-only mode, not moving (score=%d)", tag, res.UID, res.SpamScore)
		} else {
			if err := im.MoveMessage(res.UID, inboxCfg.Spam); err != nil {
				logx.Errorf("%s UID=%d failed to move message: %v — not advancing checkpoint", tag, res.UID, err)
				return
			}
			logx.Debugf("%s UID=%d moved to %s (score=%d)", tag, res.UID, inboxCfg.Spam, res.SpamScore)
		}
	}

	if err := mgr.Complete(uint32(res.UID)); err != nil {
		logx.Errorf("%s UID=%d could not advance checkpoint: %v", tag, res.UID, err)
	}
}

// reconnect dials a fresh IMAP connection with exponential back-off.
// Returns (nil, backoff) if ctx is cancelled before a successful connect.
func reconnect(ctx context.Context, inboxCfg app.Inbox, backoff time.Duration, tag string) (*imap.Imap, time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return nil, backoff
		case <-time.After(backoff):
		}
		im, err := imap.NewForIdle(inboxCfg)
		if err == nil {
			logx.Infof("%s reconnected successfully", tag)
			return im, time.Second
		}
		logx.Errorf("%s reconnect failed: %v — retrying in %s", tag, err, backoff)
		if backoff < maxReconnectBackoff {
			backoff *= 2
		}
	}
}

func processInbox(ctx app.Context, inboxCfg app.Inbox, prov app.Provider) {

	var err error
	var p provider.Provider
	var im *imap.Imap

	logx.Infof("Handling %s", inboxCfg.Username)
	logx.Debugf("Run triggered at %s for %s (host=%s inbox=%s)", time.Now().UTC().Format(time.RFC3339), inboxCfg.Username, inboxCfg.Host, inboxCfg.Inbox)

	cp, err := checkpoint.Load(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox)
	if err != nil {
		logx.Errorf("Could not load checkpoint: %v\n", err)
		return
	}
	if cp == nil {
		logx.Debugf("No existing checkpoint found for %s", inboxCfg.Username)
		logx.Debugf("Checking mailbox %s (%s) with no checkpoint", inboxCfg.Username, inboxCfg.Inbox)
	} else {
		logx.Debugf("Loaded checkpoint for %s: UIDValidity=%d LastUID=%d", inboxCfg.Username, cp.UIDValidity, cp.LastUID)
		logx.Debugf("Checking mailbox %s (%s) since UID %d", inboxCfg.Username, inboxCfg.Inbox, cp.LastUID)
	}

	if im, err = imap.New(inboxCfg); err != nil {
		logx.Errorf("Could not load imap: %v\n", err)
		return
	}
	defer im.Close()

	currentUIDValidity := im.GetUIDValidity()

	// First run: no checkpoint exists yet — establish the baseline UID.
	if cp == nil {
		logx.Infof("First run for %s: establishing checkpoint (no messages processed this run)", inboxCfg.Username)
		maxUID, err := im.GetMaxUID()
		if err != nil {
			logx.Errorf("Could not get max UID: %v\n", err)
			return
		}
		if err = checkpoint.Save(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox, &checkpoint.Checkpoint{
			UIDValidity: currentUIDValidity,
			LastUID:     uint32(maxUID),
		}); err != nil {
			logx.Errorf("Could not save checkpoint: %v\n", err)
		}
		logx.Infof("Checkpoint initialised at UID %d (UIDValidity=%d)", maxUID, currentUIDValidity)
		return
	}

	// UIDVALIDITY changed: UIDs are no longer meaningful — reset the checkpoint.
	if cp.UIDValidity != currentUIDValidity {
		logx.Warnf("UIDVALIDITY changed for %s (%d → %d): resetting checkpoint", inboxCfg.Username, cp.UIDValidity, currentUIDValidity)
		maxUID, err := im.GetMaxUID()
		if err != nil {
			logx.Errorf("Could not get max UID after UIDVALIDITY change: %v\n", err)
			return
		}
		if err = checkpoint.Save(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox, &checkpoint.Checkpoint{
			UIDValidity: currentUIDValidity,
			LastUID:     uint32(maxUID),
		}); err != nil {
			logx.Errorf("Could not save checkpoint after UIDVALIDITY reset: %v\n", err)
		}
		logx.Infof("Checkpoint reset to UID %d (UIDValidity=%d)", maxUID, currentUIDValidity)
		return
	}

	// Incremental run: process only messages newer than the last checkpoint.
	sinceUID := goimap.UID(cp.LastUID)
	msgs, err := im.LoadMessages(sinceUID)
	if err != nil {
		logx.Errorf("Could not load messages: %v\n", err)
		return
	}
	logx.Infof("Loaded %d new messages since UID %d", len(msgs), sinceUID)
	if len(msgs) == 0 {
		logx.Debugf("Mailbox %s (%s) check complete; no new UID found", inboxCfg.Username, inboxCfg.Inbox)
	} else {
		newestUID := cp.LastUID
		for _, m := range msgs {
			if m.UID > newestUID {
				newestUID = m.UID
			}
		}
		logx.Debugf("Mailbox %s (%s) newest UID found: %d", inboxCfg.Username, inboxCfg.Inbox, newestUID)
	}

	p, err = provider.New(prov.Type)
	if err != nil {
		logx.Errorf("Could not load provider: %v\n", err)
		return
	}

	if err = p.Init(prov.Config); err != nil {
		logx.Errorf("Could not init provider: %v\n", err)
		return
	}

	moved := 0
	for _, m := range msgs {
		if wl, ok := ctx.Config.Whitelists[inboxCfg.Whitelist]; ok {
			if isTrustedSender(m.From, wl) {
				logx.Debugf("Skipping message #%d (%s) because of trusted sender (%s)", m.UID, m.Subject, m.From)
				// Advance checkpoint for skipped (trusted) messages.
				if err = checkpoint.Save(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox, &checkpoint.Checkpoint{
					UIDValidity: currentUIDValidity,
					LastUID:     uint32(m.UID),
				}); err != nil {
					logx.Errorf("Could not save checkpoint for UID %d: %v\n", m.UID, err)
				}
				continue
			}
		}

		n, err := p.Analyze(m)
		if err != nil {
			logx.Errorf("Could not analyze message #%d (%s): %v\n", m.UID, m.Subject, err)
			logx.Infof("Stopping inbox processing at UID %d; will retry from there on next run", m.UID)
			break
		}
		logx.Debugf("Spam score of message #%d (%s): %d/100", m.UID, m.Subject, n)

		if n >= inboxCfg.MinScore {
			if ctx.Options.AnalyzeOnly {
				logx.Debugf("Analyze only mode, not moving message #%d", m.UID)
			} else {
				if err = im.MoveMessage(m.UID, inboxCfg.Spam); err != nil {
					logx.Errorf("Could not move message #%d (%s): %v\n", m.UID, m.Subject, err)
					logx.Infof("Stopping inbox processing at UID %d; will retry from there on next run", m.UID)
					break
				}
				moved++
			}
		}

		// Advance checkpoint only after the message was fully and successfully processed.
		if err = checkpoint.Save(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox, &checkpoint.Checkpoint{
			UIDValidity: currentUIDValidity,
			LastUID:     uint32(m.UID),
		}); err != nil {
			logx.Errorf("Could not save checkpoint for UID %d: %v\n", m.UID, err)
			logx.Infof("Stopping inbox processing at UID %d; will retry from there on next run", m.UID)
			break
		}
	}
	logx.Infof("Moved %d messages", moved)
}

// isTrustedSender checks whether sender matches any entry in a whitelist.
func isTrustedSender(from string, wl []regexp.Regexp) bool {
	for _, rgx := range wl {
		if rgx.Match([]byte(from)) {
			return true
		}
	}
	return false
}

func logTag(host, username, inbox string) string {
	return "[" + host + " " + username + " " + inbox + "]"
}
