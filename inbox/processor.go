package inbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/checkpoint"
	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/internal/dispatcher"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
	"github.com/dominicgisler/imap-spam-cleaner/storage"
	goimap "github.com/emersion/go-imap/v2"
)

func processInbox(ctx app.Context, inboxCfg app.Inbox, prov app.Provider) {
	processInboxInternal(ctx, inboxCfg, prov, nil, context.Background())
}

func processInboxInternal(appCtx app.Context, inboxCfg app.Inbox, prov app.Provider, disp *dispatcher.Dispatcher, runCtx context.Context) {

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

	spamFolder := inboxCfg.Spam

	// Prefer a stored/spillover mapping for the spam mailbox if available.
	dbPath := storage.SentDBPath(inboxCfg.Host, inboxCfg.Username)
	var st *storage.Storage
	if sst, ok := appCtx.Storages[dbPath]; ok && sst != nil {
		st = sst
		if storedSpam, err := st.GetMailbox("spam"); err == nil && storedSpam != "" {
			if actual, ok := im.FindMailbox(storedSpam); ok {
				spamFolder = actual
				logx.Debugf("Using stored spam mailbox %q for %s", actual, inboxCfg.Username)
			} else {
				logx.Debugf("Stored spam mailbox %q not found for %s", storedSpam, inboxCfg.Username)
			}
		}
	}

	// If no stored mapping or stored mapping not usable, fall back to configured/detected logic.
	if spamFolder == inboxCfg.Spam {
		if actual, ok := im.FindMailbox(inboxCfg.Spam); ok {
			spamFolder = actual
		} else if detected, ok := im.DetectSpamMailbox(); ok {
			logx.Warnf("Configured spam mailbox %q not found among available mailboxes; auto-selecting detected spam mailbox %q", inboxCfg.Spam, detected)
			spamFolder = detected
			if st != nil {
				if storedSpam, _ := st.GetMailbox("spam"); storedSpam != detected {
					if err := st.SetMailbox("spam", detected); err != nil {
						logx.Errorf("Could not persist detected spam mailbox for %s: %v", inboxCfg.Username, err)
					} else {
						logx.Infof("Persisted detected spam mailbox %q for %s", detected, inboxCfg.Username)
					}
				}
			}
		} else {
			logx.Warnf("Configured spam mailbox %q not found among available mailboxes; message moves may fail", inboxCfg.Spam)
		}
	}

	currentUIDValidity := im.GetUIDValidity()

	// If no checkpoint exists, establish it for incremental processing
	if cp == nil {
		logx.Infof("Establishing checkpoint for %s (first incremental run)", inboxCfg.Username)
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
		logx.Infof("Checkpoint initialised at UID %d (UIDValidity=%d); incremental processing will start on next run", maxUID, currentUIDValidity)
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
	skippedReasons := make(map[uint32]string)
	for _, m := range msgs {
		if mgr.IsAlreadyProcessed(uint32(m.UID)) {
			logx.Debugf("Skipping already processed message by checkpoint #%d (%s)", m.UID, m.Subject)
			skippedUIDs = append(skippedUIDs, uint32(m.UID))
			skippedReasons[uint32(m.UID)] = "already processed by checkpoint"
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
			skippedReasons[uint32(m.UID)] = "already processed by uid marker"
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
				logx.Debugf("Skipping message #%d (%s) because of trusted sender (%s)", m.UID, m.Subject, m.From)
				skippedUIDs = append(skippedUIDs, uint32(m.UID))
				skippedReasons[uint32(m.UID)] = "whitelisted by trusted sender pattern"
				continue
			}
		}

		if inboxCfg.EnableSentWhitelist {
			dbPath := storage.SentDBPath(inboxCfg.Host, inboxCfg.Username)
			if st, ok := appCtx.Storages[dbPath]; ok && st != nil {
				known, err := st.HasContact(m.From)
				if err != nil {
					logx.Errorf("Could not check sent-folder contact memory for %s: %v", m.From, err)
				} else if known {
					m.Whitelisted = true
					m.LLMReason = "whitelisted by sent-folder contact memory"
					logx.Debugf("Skipping message #%d (%s) because sender %s is in sent-folder contact memory", m.UID, m.Subject, m.From)
					skippedUIDs = append(skippedUIDs, uint32(m.UID))
					skippedReasons[uint32(m.UID)] = "whitelisted by sent-folder contact memory"
					continue
				}
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
		logx.Infof("Spam score for message #%d: %d/100; Phishing=%t; IsSpam=%t; From=%s; Subject=%s; Reason=%s", m.UID, analysis.Score, analysis.IsPhishing, analysis.IsSpam, m.From, m.Subject, analysis.Reason)

		// Move to spam if the LLM marks it as phishing or if the spam score
		// meets/exceeds the configured minimum.
		if analysis.Score >= inboxCfg.MinScore || analysis.IsPhishing || analysis.IsSpam {
			if appCtx.Options.AnalyzeOnly {
				logx.Debugf("Analyze only mode, not moving message #%d", m.UID)
			} else {
				if err = im.MoveMessage(m.UID, spamFolder); err != nil {
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
		// Build human-readable list with reasons
		entries := make([]string, 0, len(skippedUIDs))
		for _, uid := range skippedUIDs {
			reason := skippedReasons[uid]
			entries = append(entries, fmt.Sprintf("%d (%s)", uid, reason))
		}
		logx.Infof("Skipped message UIDs: %s", strings.Join(entries, ", "))
	}
	logx.Infof("Processed %d messages, moved %d messages", len(processedUIDs), moved)
}
