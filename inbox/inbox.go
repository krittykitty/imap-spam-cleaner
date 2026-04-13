package inbox

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/checkpoint"
	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/internal/dispatcher"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
	"github.com/dominicgisler/imap-spam-cleaner/storage"
	goimap "github.com/emersion/go-imap/v2"
	"github.com/go-co-op/gocron/v2"
)

func Schedule(ctx app.Context) {

	s, err := gocron.NewScheduler()
	if err != nil {
		logx.Errorf("Could not create scheduler: %v", err)
		return
	}

	shutdownCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Build dispatchers for providers that are used by any IDLE inboxes.
	dispatchers := buildDispatchers(ctx, shutdownCtx)

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
			// pass a dispatcher for the provider if one exists (may be nil)
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

		if inbox.EnableSentWhitelist {
			if err := syncSentFolder(ctx, inbox); err != nil {
				logx.Errorf("Could not perform initial sent-folder sync for %s: %v", inbox.Username, err)
			}
			logx.Infof("Scheduling sent-folder sync for %s (%s)", inbox.Username, inbox.SentFolderSchedule)
			if _, err = s.NewJob(
				gocron.CronJob(inbox.SentFolderSchedule, false),
				gocron.NewTask(syncSentFolder, ctx, inbox),
			); err != nil {
				logx.Errorf("Could not schedule sent-folder sync for %s (%s): %v", inbox.Username, inbox.SentFolderSchedule, err)
				continue
			}
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

	// Close all dispatchers to ensure workers exit cleanly.
	for _, d := range dispatchers {
		if d != nil {
			d.Close()
		}
	}

	if err = s.Shutdown(); err != nil {
		logx.Errorf("Could not shutdown scheduler: %v ", err)
	}
}

// initialPopulation seeds the recent store with the last N messages from the
// inbox and the sent folder (if enabled), and writes a simple initial
// consolidation summary. This is intentionally lightweight; a later step may
// replace the summary with an LLM-generated consolidation.
func initialPopulation(ctx app.Context, inboxCfg app.Inbox) error {
	recentPath := storage.RecentDBPath(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox)

	recentStore, err := storage.NewRecent(recentPath)
	if err != nil {
		return err
	}
	defer recentStore.Close()

	// helper to seed from a mailbox (inbox or sent)
	seedFromMailbox := func(cfg app.Inbox, maxMessages int) ([]string, error) {
		im, err := imap.New(cfg)
		if err != nil {
			return nil, err
		}
		defer im.Close()

		maxUID, err := im.GetMaxUID()
		if err != nil {
			return nil, err
		}
		var sinceUID goimap.UID
		if maxUID > goimap.UID(maxMessages) {
			sinceUID = maxUID - goimap.UID(maxMessages)
		} else {
			sinceUID = 0
		}
		msgs, err := im.LoadMessages(sinceUID)
		if err != nil {
			return nil, err
		}

		subjects := make([]string, 0, len(msgs))
		for _, m := range msgs {
			snippet := m.TextBody
			if snippet == "" {
				snippet = m.HtmlBody
			}
			if len(snippet) > 250 {
				snippet = snippet[:250]
			}
			var spamScore *float64
			if m.SpamScoreValid {
				spamScore = &m.SpamScore
			}
			if err := recentStore.UpsertMessage(storage.RecentMessage{
				UID:         uint32(m.UID),
				From:        m.From,
				To:          m.To,
				Subject:     m.Subject,
				Snippet:     snippet,
				Date:        m.Date,
				SpamScore:   spamScore,
				LLMReason:   m.LLMReason,
				Whitelisted: m.Whitelisted,
			}); err != nil {
				logx.Errorf("initial population: could not insert message UID %d: %v", m.UID, err)
			}
			subjects = append(subjects, m.Subject)
		}
		return subjects, nil
	}

	// seed inbox
	inboxCfgCopy := inboxCfg
	inboxSubjects, err := seedFromMailbox(inboxCfgCopy, 25)
	if err != nil {
		logx.Errorf("initial population: failed seeding inbox %s: %v", inboxCfg.Username, err)
	}

	// seed sent folder if enabled
	sentSubjects := []string{}
	if inboxCfg.EnableSentWhitelist && inboxCfg.SentFolder != "" {
		sentCfg := inboxCfg
		sentCfg.Inbox = inboxCfg.SentFolder
		sentSubjects, err = seedFromMailbox(sentCfg, 25)
		if err != nil {
			logx.Errorf("initial population: failed seeding sent folder %s: %v", inboxCfg.Username, err)
		}
	}

	// write a simple consolidation: list top subjects and counts of senders
	var summaryBuilder strings.Builder
	summaryBuilder.WriteString("Initial consolidation: recent activity summary.\n")
	if len(inboxSubjects) > 0 {
		summaryBuilder.WriteString("Inbox recent subjects:\n")
		for i, s := range inboxSubjects {
			if i >= 10 {
				break
			}
			summaryBuilder.WriteString("- ")
			summaryBuilder.WriteString(s)
			summaryBuilder.WriteString("\n")
		}
	}
	if len(sentSubjects) > 0 {
		summaryBuilder.WriteString("Sent recent subjects:\n")
		for i, s := range sentSubjects {
			if i >= 10 {
				break
			}
			summaryBuilder.WriteString("- ")
			summaryBuilder.WriteString(s)
			summaryBuilder.WriteString("\n")
		}
	}

	if err := recentStore.SaveConsolidation(summaryBuilder.String()); err != nil {
		return err
	}

	logx.Infof("Initial population complete for %s: stored %d inbox subjects, %d sent subjects", inboxCfg.Username, len(inboxSubjects), len(sentSubjects))
	return nil
}

func RunAllInboxes(ctx app.Context) {
	for i, inbox := range ctx.Config.Inboxes {
		logx.Infof("Processing inbox %s", inbox.Username)
		prov, ok := ctx.Config.Providers[inbox.Provider]
		if !ok {
			logx.Errorf("Invalid provider %s for inbox %d", inbox.Provider, i)
			continue
		}
		if inbox.EnableSentWhitelist {
			if err := syncSentFolder(ctx, inbox); err != nil {
				logx.Errorf("Sent-folder sync failed for %s: %v", inbox.Username, err)
			}
		}
		processInbox(ctx, inbox, prov)
	}
}

// buildDispatchers creates one dispatcher per provider that is used by at
// least one IDLE-enabled inbox. The returned map keys are provider names as
// referenced in the config (not provider types).
func buildDispatchers(ctx app.Context, shutdownCtx context.Context) map[string]*dispatcher.Dispatcher {
	used := make(map[string]struct{})
	for _, inbox := range ctx.Config.Inboxes {
		if inbox.EnableIdle {
			used[inbox.Provider] = struct{}{}
		}
	}

	dispatchers := make(map[string]*dispatcher.Dispatcher)
	for name := range used {
		prov, ok := ctx.Config.Providers[name]
		if !ok {
			logx.Errorf("buildDispatchers: invalid provider reference %s", name)
			continue
		}
		d, err := dispatcher.New(shutdownCtx, prov.Type, prov.Config, prov.Concurrency, prov.RateLimit)
		if err != nil {
			logx.Errorf("Could not create dispatcher for provider %s: %v", name, err)
			dispatchers[name] = nil
			continue
		}
		dispatchers[name] = d
		logx.Debugf("Created dispatcher for provider %s (concurrency=%d, rate_limit=%.2f)", name, prov.Concurrency, prov.RateLimit)
	}
	return dispatchers
}

func processInbox(ctx app.Context, inboxCfg app.Inbox, prov app.Provider) {
	processInboxInternal(ctx, inboxCfg, prov, nil, context.Background())
}

func processInboxInternal(appCtx app.Context, inboxCfg app.Inbox, prov app.Provider, disp *dispatcher.Dispatcher, runCtx context.Context) {

	var err error
	var p provider.Provider
	var im *imap.Imap
	var recentStore *storage.RecentStore

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

	recentPath := storage.RecentDBPath(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox)
	recentStore, err = storage.NewRecent(recentPath)
	if err != nil {
		logx.Errorf("Could not open recent message store for %s: %v", inboxCfg.Username, err)
	} else {
		defer recentStore.Close()
	}

	snippetFromMessage := func(msg imap.Message, maxBytes int) string {
		if msg.TextBody != "" {
			if len(msg.TextBody) > maxBytes {
				return msg.TextBody[:maxBytes]
			}
			return msg.TextBody
		}
		if msg.HtmlBody != "" {
			if len(msg.HtmlBody) > maxBytes {
				return msg.HtmlBody[:maxBytes]
			}
			return msg.HtmlBody
		}
		if len(msg.Raw) > 0 {
			raw := string(msg.Raw)
			if len(raw) > maxBytes {
				return raw[:maxBytes]
			}
			return raw
		}
		return ""
	}

	storeRecentMessage := func(msg imap.Message) {
		if recentStore == nil {
			return
		}
		snippet := snippetFromMessage(msg, 250)
		var spamScore *float64
		if msg.SpamScoreValid {
			spamScore = &msg.SpamScore
		}
		if err := recentStore.UpsertMessage(storage.RecentMessage{
			UID:         uint32(msg.UID),
			From:        msg.From,
			To:          msg.To,
			Subject:     msg.Subject,
			Snippet:     snippet,
			Date:        msg.Date,
			SpamScore:   spamScore,
			LLMReason:   msg.LLMReason,
			Whitelisted: msg.Whitelisted,
		}); err != nil {
			logx.Errorf("Could not store recent message for UID %d: %v", msg.UID, err)
		}
	}

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
		} else {
			// attempt to seed recent-message memory on first run; non-fatal
			if err := initialPopulation(appCtx, inboxCfg); err != nil {
				logx.Errorf("Initial population failed for %s: %v", inboxCfg.Username, err)
			}
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

	providerInitialized := false

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
				m.Whitelisted = true
				m.LLMReason = "whitelisted by trusted sender pattern"
				storeRecentMessage(m)
				logx.Debugf("Skipping message #%d (%s) because of trusted sender (%s)", m.UID, m.Subject, m.From)
				skippedUIDs = append(skippedUIDs, uint32(m.UID))
				continue
			}
		}

		if inboxCfg.EnableSentWhitelist {
			dbPath := storage.DBPath(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox)
			if st, ok := appCtx.Storages[dbPath]; ok && st != nil {
				known, err := st.HasContact(m.From)
				if err != nil {
					logx.Errorf("Could not check sent-folder contact memory for %s: %v", m.From, err)
				} else if known {
					m.Whitelisted = true
					m.LLMReason = "whitelisted by sent-folder contact memory"
					storeRecentMessage(m)
					logx.Debugf("Skipping message #%d (%s) because sender %s is in sent-folder contact memory", m.UID, m.Subject, m.From)
					skippedUIDs = append(skippedUIDs, uint32(m.UID))
					continue
				}
			}
		}

		if recentStore != nil {
			m.Context, err = recentStore.GetConsolidatedContext(10, 90*24*time.Hour)
			if err != nil {
				logx.Errorf("Could not get consolidated context for %s: %v", inboxCfg.Username, err)
			}
		}

		var analysis provider.AnalysisResponse
		if disp == nil {
			if !providerInitialized {
				p, err = provider.New(prov.Type)
				if err != nil {
					logx.Errorf("Could not load provider: %v", err)
					return
				}
				if err = p.Init(prov.Config); err != nil {
					logx.Errorf("Could not init provider: %v", err)
					return
				}
				providerInitialized = true
			}

			analysis, err = p.Analyze(m)
		} else {
			// respect explicit 0 (disable retries) and guard nil pointers
			maxRetries := 3
			if inboxCfg.MaxRetries != nil {
				maxRetries = *inboxCfg.MaxRetries
			}
			analysis, err = disp.Analyze(runCtx, m, maxRetries)
		}

		if err != nil {
			logx.Errorf("Could not analyze message #%d (%s): %v\n", m.UID, m.Subject, err)
			logx.Infof("Continuing after failed analysis for UID %d (marked processed, will not retry)", m.UID)
			processedUIDs = append(processedUIDs, uint32(m.UID))
			continue
		}
		m.SpamScore = float64(analysis.Score)
		m.SpamScoreValid = true
		m.LLMReason = analysis.Reason
		m.Whitelisted = false
		storeRecentMessage(m)
		logx.Debugf("Spam score of message #%d (%s): %d/100", m.UID, m.Subject, analysis.Score)

		if analysis.Score >= inboxCfg.MinScore {
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
	if recentStore != nil && shouldRunConsolidation(recentStore, inboxCfg, len(processedUIDs)) {
		if err := runConsolidation(appCtx, inboxCfg, recentStore, p, prov); err != nil {
			logx.Errorf("Could not consolidate recent context for %s: %v", inboxCfg.Username, err)
		}
	}
	logx.Infof("Processed %d messages, moved %d messages", len(processedUIDs), moved)
}

func shouldRunConsolidation(store *storage.RecentStore, cfg app.Inbox, processed int) bool {
	if cfg.RecentConsolidationEvery <= 0 {
		cfg.RecentConsolidationEvery = 50
	}
	if processed >= cfg.RecentConsolidationEvery {
		return true
	}
	meta, err := store.GetLatestConsolidationMeta()
	if err != nil {
		logx.Errorf("Could not read consolidation metadata: %v", err)
		return processed > 0
	}
	if meta.CreatedAt.IsZero() {
		return true
	}
	if cfg.RecentConsolidationInterval <= 0 {
		cfg.RecentConsolidationInterval = 24 * time.Hour
	}
	return time.Since(meta.CreatedAt) >= cfg.RecentConsolidationInterval
}

func runConsolidation(ctx app.Context, inboxCfg app.Inbox, recentStore *storage.RecentStore, p provider.Provider, prov app.Provider) error {
	// Collect structured recent messages and previous consolidation
	messages, err := recentStore.GetRecentMessages(50, time.Now().UTC().Add(-90*24*time.Hour))
	if err != nil {
		return err
	}
	prevConsolidation, err := recentStore.GetLatestConsolidation()
	if err != nil {
		return err
	}
	if len(messages) == 0 && prevConsolidation == "" {
		logx.Infof("No recent memory found for %s; running initial population bootstrap", inboxCfg.Username)
		if err := initialPopulation(ctx, inboxCfg); err != nil {
			return fmt.Errorf("initial population bootstrap failed: %w", err)
		}
		messages, err = recentStore.GetRecentMessages(50, time.Now().UTC().Add(-90*24*time.Hour))
		if err != nil {
			return err
		}
		prevConsolidation, err = recentStore.GetLatestConsolidation()
		if err != nil {
			return err
		}
	}
	if len(messages) == 0 && prevConsolidation == "" {
		return nil
	}

	// Build list of latest senders
	senderSet := make(map[string]struct{})
	senderList := make([]string, 0, len(messages))
	for _, m := range messages {
		if m.From == "" {
			continue
		}
		if _, ok := senderSet[m.From]; !ok {
			senderSet[m.From] = struct{}{}
			senderList = append(senderList, m.From)
		}
	}
	latestSenders := strings.Join(senderList, ", ")

	// Map messages to template struct
	tplMsgs := make([]provider.ConsolidationMessage, 0, len(messages))
	for _, m := range messages {
		score := "n/a"
		if m.SpamScore != nil {
			score = fmt.Sprintf("%.1f", *m.SpamScore)
		}
		tplMsgs = append(tplMsgs, provider.ConsolidationMessage{
			From:      m.From,
			To:        m.To,
			Subject:   m.Subject,
			Snippet:   m.Snippet,
			SpamScore: score,
			LLMReason: m.LLMReason,
		})
	}

	consolidationProvider := prov
	if inboxCfg.ConsolidationProvider != "" {
		cp, ok := ctx.Config.Providers[inboxCfg.ConsolidationProvider]
		if !ok {
			return fmt.Errorf("invalid consolidation provider %s for inbox %s", inboxCfg.ConsolidationProvider, inboxCfg.Username)
		}
		consolidationProvider = cp
	}

	// Build consolidation config: allow consolidation_ overrides inside provider config
	consolidationConfig := make(map[string]string)
	for k, v := range consolidationProvider.Config {
		// copy base keys, including non-prefixed ones
		if !strings.HasPrefix(k, "consolidation_") {
			consolidationConfig[k] = v
		}
	}
	// Apply consolidation_ overrides
	for k, v := range consolidationProvider.Config {
		if strings.HasPrefix(k, "consolidation_") {
			newKey := strings.TrimPrefix(k, "consolidation_")
			consolidationConfig[newKey] = v
		}
	}

	// Decide whether to reuse the analysis provider instance or create a new one
	var pcons provider.Provider
	hasOverrides := false
	for k := range consolidationProvider.Config {
		if strings.HasPrefix(k, "consolidation_") {
			hasOverrides = true
			break
		}
	}
	if !hasOverrides && inboxCfg.ConsolidationProvider == "" && p != nil {
		// reuse analysis provider instance
		pcons = p
	} else {
		pcons, err = provider.New(consolidationProvider.Type)
		if err != nil {
			return err
		}
		if err = pcons.Init(consolidationConfig); err != nil {
			return err
		}
	}

	var summary string
	// Try new ConsolidateVars API first
	if consolidator, ok := pcons.(interface {
		ConsolidateVars(provider.ConsolidationPromptVars) (string, error)
	}); ok {
		summary, err = consolidator.ConsolidateVars(provider.ConsolidationPromptVars{
			Messages:              tplMsgs,
			LatestSenders:         latestSenders,
			PreviousConsolidation: prevConsolidation,
		})
		if err != nil {
			return err
		}
	} else if consolidator, ok := pcons.(interface{ Consolidate(string) (string, error) }); ok {
		// fallback to legacy string-based consolidation
		contextStr, err := recentStore.GetConsolidatedContext(50, 90*24*time.Hour)
		if err != nil {
			return err
		}
		summary, err = consolidator.Consolidate(contextStr)
		if err != nil {
			return err
		}
	} else {
		summary = "Consolidated recent context:\n" + prevConsolidation
	}

	if summary == "" {
		return nil
	}

	return recentStore.SaveConsolidation(summary)
}
