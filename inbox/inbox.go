package inbox

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/checkpoint"
	"github.com/dominicgisler/imap-spam-cleaner/dispatcher"
	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
	goimap "github.com/emersion/go-imap/v2"
	"github.com/go-co-op/gocron/v2"
)

// buildDispatchers creates one Dispatcher per provider that has at least one
// IDLE-enabled inbox. Providers only used by cron-scheduled inboxes do not get
// a dispatcher (they create a local provider instance per run).
func buildDispatchers(cfg *app.Config) map[string]*dispatcher.Dispatcher {
	idleProviders := make(map[string]struct{})
	for _, inbox := range cfg.Inboxes {
		if inbox.EnableIdle {
			idleProviders[inbox.Provider] = struct{}{}
		}
	}

	dispatchers := make(map[string]*dispatcher.Dispatcher, len(idleProviders))
	for name := range idleProviders {
		prov := cfg.Providers[name]
		concurrency := prov.Concurrency
		if concurrency < 1 {
			concurrency = 1
		}
		d, err := dispatcher.New(prov.Type, prov.Config, concurrency, prov.RateLimit)
		if err != nil {
			logx.Errorf("Could not create dispatcher for provider %s: %v", name, err)
			continue
		}
		logx.Debugf("Created dispatcher for provider %s (concurrency=%d rate_limit=%.2f)",
			name, concurrency, prov.RateLimit)
		dispatchers[name] = d
	}
	return dispatchers
}

func Schedule(ctx app.Context) {

	s, err := gocron.NewScheduler()
	if err != nil {
		logx.Errorf("Could not create scheduler: %v", err)
		return
	}

	shutdownCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dispatchers := buildDispatchers(ctx.Config)
	defer func() {
		for _, d := range dispatchers {
			d.Stop()
		}
	}()

	jobs := 0
	idleCount := 0
	for i, inbox := range ctx.Config.Inboxes {
		prov, ok := ctx.Config.Providers[inbox.Provider]
		if !ok {
			logx.Errorf("Invalid provider %s for inbox %d", inbox.Provider, i)
			continue
		}

		if inbox.EnableIdle {
			logx.Infof("Skipping cron for idle inbox %s", inbox.Username)
			go StartIdle(shutdownCtx, ctx, inbox, prov, dispatchers[inbox.Provider])
			idleCount++
			continue
		}

		logx.Infof("Scheduling inbox %s (%s)", inbox.Username, inbox.Schedule)
		if _, err = s.NewJob(
			gocron.CronJob(inbox.Schedule, false),
			gocron.NewTask(processInbox, ctx, inbox, prov),
		); err != nil {
			logx.Errorf("Could not schedule inbox %s (%s): %v", inbox.Username, inbox.Schedule, err)
			continue
		}
		jobs++
	}

	logx.Debugf("Scheduled %d inbox jobs, started %d IDLE watchers", jobs, idleCount)
	logx.Debugf("Starting scheduler")
	s.Start()
	logx.Debugf("Scheduler started")

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	sig := <-ch
	logx.Debugf("Received %s, shutting down", sig.String())

	cancel()

	if err = s.Shutdown(); err != nil {
		logx.Errorf("Could not shutdown scheduler: %v ", err)
	}
}

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

// processInbox is the cron-path entry point. It creates a fresh local provider
// instance for each run and processes the inbox sequentially.
func processInbox(appCtx app.Context, inboxCfg app.Inbox, prov app.Provider) {
	processInboxInternal(context.Background(), appCtx, inboxCfg, prov, nil)
}

// processInboxInternal is the shared implementation used by both the cron and
// IDLE paths.
//
// When disp is non-nil (IDLE path), analysis is delegated to the dispatcher,
// which provides rate-limiting and exponential-backoff retry; the local
// provider instance is not created.  When disp is nil (cron path), a local
// provider is created and analysis is performed sequentially without retries.
func processInboxInternal(goCtx context.Context, appCtx app.Context, inboxCfg app.Inbox, prov app.Provider, disp *dispatcher.Dispatcher) {

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

	mgr := checkpoint.NewManager(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox, cp)

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
		newestUID := goimap.UID(cp.LastUID)
		loadedUIDs := make([]uint32, 0, len(msgs))
		for _, m := range msgs {
			loadedUIDs = append(loadedUIDs, uint32(m.UID))
			if m.UID > newestUID {
				newestUID = m.UID
			}
		}
		logx.Debugf("Mailbox %s (%s) newest UID found: %d", inboxCfg.Username, inboxCfg.Inbox, uint32(newestUID))
		logx.Debugf("Loaded message UIDs for processing: %v", loadedUIDs)
	}

	// Initialise a local provider only when the dispatcher is not available
	// (cron path). The dispatcher manages its own pool of initialised providers.
	if disp == nil {
		p, err = provider.New(prov.Type)
		if err != nil {
			logx.Errorf("Could not load provider: %v\n", err)
			return
		}

		if err = p.Init(prov.Config); err != nil {
			logx.Errorf("Could not init provider: %v\n", err)
			return
		}
	}

	moved := 0
	processedUIDs := make([]uint32, 0, len(msgs))
	skippedUIDs := make([]uint32, 0, len(msgs))
	for _, m := range msgs {
		if mgr.IsAlreadyProcessed(uint32(m.UID)) {
			logx.Debugf("Skipping already processed message by checkpoint #%d (%s)", m.UID, m.Subject)
			skippedUIDs = append(skippedUIDs, uint32(m.UID))
			continue
		}

		marked, markErr := checkpoint.TryMarkUIDProcessed(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox, uint32(m.UID))
		if markErr != nil {
			logx.Errorf("Could not mark UID %d as processed: %v", m.UID, markErr)
			continue
		}
		if !marked {
			logx.Debugf("Skipping already processed message by uid marker #%d (%s)", m.UID, m.Subject)
			skippedUIDs = append(skippedUIDs, uint32(m.UID))
			continue
		}

		if err = mgr.Complete(uint32(m.UID)); err != nil {
			logx.Errorf("Could not mark message #%d as completed: %v", m.UID, err)
		}
		if err = checkpoint.Save(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox, &checkpoint.Checkpoint{
			UIDValidity: currentUIDValidity,
			LastUID:     mgr.LastUID(),
		}); err != nil {
			logx.Errorf("Could not save checkpoint for UID %d: %v\n", m.UID, err)
		}

		if wl, ok := appCtx.Config.Whitelists[inboxCfg.Whitelist]; ok {
			trustedSender := false
			for _, rgx := range wl {
				if rgx.Match([]byte(m.From)) {
					trustedSender = true
					break
				}
			}
			if trustedSender {
				logx.Debugf("Skipping message #%d (%s) because of trusted sender (%s)", m.UID, m.Subject, m.From)
				skippedUIDs = append(skippedUIDs, uint32(m.UID))
				continue
			}
		}

		var n int
		if disp != nil {
			n, err = disp.Analyze(goCtx, m, inboxCfg.MaxRetries)
		} else {
			n, err = p.Analyze(m)
		}
		if err != nil {
			logx.Errorf("Could not analyze message #%d (%s): %v\n", m.UID, m.Subject, err)
			logx.Infof("Continuing after failed analysis for UID %d (marked processed, will not retry)", m.UID)
			processedUIDs = append(processedUIDs, uint32(m.UID))
			continue
		}
		logx.Debugf("Spam score of message #%d (%s): %d/100", m.UID, m.Subject, n)

		if n >= inboxCfg.MinScore {
			if appCtx.Options.AnalyzeOnly {
				logx.Debugf("Analyze only mode, not moving message #%d", m.UID)
			} else {
				if err = im.MoveMessage(m.UID, inboxCfg.Spam); err != nil {
					logx.Errorf("Could not move message #%d (%s): %v\n", m.UID, m.Subject, err)
					logx.Infof("Continuing after failed move for UID %d (marked processed, will not retry)", m.UID)
					processedUIDs = append(processedUIDs, uint32(m.UID))
					continue
				}
				moved++
			}
		}

		processedUIDs = append(processedUIDs, uint32(m.UID))
	}
	if len(processedUIDs) > 0 {
		logx.Debugf("Processed message UIDs: %v", processedUIDs)
	}
	if len(skippedUIDs) > 0 {
		logx.Debugf("Skipped message UIDs: %v", skippedUIDs)
	}
	logx.Infof("Processed %d messages, moved %d messages", len(processedUIDs), moved)
}
