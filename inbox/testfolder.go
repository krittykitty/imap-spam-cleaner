package inbox

import (
	"context"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/checkpoint"
	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/internal/dispatcher"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
	goimap "github.com/emersion/go-imap/v2"
)

const testFolderStartupLimit = 10

func processTestFolder(appCtx app.Context, inboxCfg app.Inbox, prov app.Provider, disp *dispatcher.Dispatcher, runCtx context.Context, startup bool) {
	if inboxCfg.TestFolder == "" {
		return
	}

	testCfg := inboxCfg
	testCfg.Inbox = inboxCfg.TestFolder
	testCfg.MinAge = 0
	testCfg.MaxAge = 0

	logx.Debugf("Test folder dry-run: checking %s (%s) folder=%s", testCfg.Username, testCfg.Host, testCfg.Inbox)

	im, err := imap.New(testCfg)
	if err != nil {
		logx.Errorf("Test folder dry-run: could not load imap for %s folder %s: %v", testCfg.Username, testCfg.Inbox, err)
		return
	}
	defer im.Close()

	cp, err := checkpoint.Load(testCfg.Host, testCfg.Username, testCfg.Inbox)
	if err != nil {
		logx.Errorf("Test folder dry-run: could not load checkpoint for %s folder %s: %v", testCfg.Username, testCfg.Inbox, err)
		return
	}

	currentUIDValidity := im.GetUIDValidity()
	messages := make([]imap.Message, 0)

	// On startup always load recent messages for the test folder so that
	// freshly copied mails are picked up for dry-run analysis. Also clear any
	// processed UID markers to force re-analysis of messages that may have
	// been copied or moved in without new UIDs.
	loadRecent := startup
	if startup {
		if err := checkpoint.ClearProcessedMarkers(testCfg.Host, testCfg.Username, testCfg.Inbox); err != nil {
			logx.Errorf("Test folder dry-run: could not clear processed uid markers for %s folder %s: %v", testCfg.Username, testCfg.Inbox, err)
		} else {
			logx.Infof("Test folder dry-run: cleared processed uid markers for %s folder %s", testCfg.Username, testCfg.Inbox)
		}
		// Reset the persisted checkpoint LastUID to 0 so incremental scans treat
		// copied messages as new (only for the test folder on startup).
		if cp != nil {
			if err := checkpoint.Save(testCfg.Host, testCfg.Username, testCfg.Inbox, &checkpoint.Checkpoint{UIDValidity: currentUIDValidity, LastUID: 0}); err != nil {
				logx.Errorf("Test folder dry-run: could not reset checkpoint for %s folder %s: %v", testCfg.Username, testCfg.Inbox, err)
			} else {
				logx.Infof("Test folder dry-run: reset checkpoint LastUID to 0 for %s folder %s", testCfg.Username, testCfg.Inbox)
			}
		}
	}
	if cp != nil && cp.UIDValidity != currentUIDValidity {
		logx.Warnf("Test folder dry-run: UIDVALIDITY changed for %s folder %s (%d -> %d), reloading latest %d", testCfg.Username, testCfg.Inbox, cp.UIDValidity, currentUIDValidity, testFolderStartupLimit)
		loadRecent = true
	}

	if loadRecent {
		uids, uidErr := im.GetLastUIDs(testFolderStartupLimit)
		if uidErr != nil {
			logx.Errorf("Test folder dry-run: could not list latest UIDs for %s folder %s: %v", testCfg.Username, testCfg.Inbox, uidErr)
			return
		}
		messages, err = im.LoadMessagesByUIDs(uids)
		if err != nil {
			logx.Errorf("Test folder dry-run: could not load latest messages for %s folder %s: %v", testCfg.Username, testCfg.Inbox, err)
			return
		}
		logx.Debugf("Test folder dry-run: loaded %d startup message(s) from %s", len(messages), testCfg.Inbox)
	} else {
		sinceUID := goimap.UID(0)
		if cp != nil {
			sinceUID = goimap.UID(cp.LastUID)
		}
		messages, err = im.LoadMessages(sinceUID)
		if err != nil {
			logx.Errorf("Test folder dry-run: could not load new messages for %s folder %s since UID %d: %v", testCfg.Username, testCfg.Inbox, sinceUID, err)
			return
		}
		logx.Debugf("Test folder dry-run: loaded %d incremental message(s) from %s since UID %d", len(messages), testCfg.Inbox, sinceUID)
	}

	if len(messages) == 0 {
		if cp == nil {
			if err := checkpoint.Save(testCfg.Host, testCfg.Username, testCfg.Inbox, &checkpoint.Checkpoint{UIDValidity: currentUIDValidity, LastUID: 0}); err != nil {
				logx.Errorf("Test folder dry-run: could not initialize empty checkpoint for %s folder %s: %v", testCfg.Username, testCfg.Inbox, err)
			}
		}
		return
	}

	var (
		p                   provider.Provider
		providerInitialized bool
		maxSeenUID          uint32
	)

	if cp != nil {
		maxSeenUID = cp.LastUID
	}

	for _, m := range messages {
		uid := uint32(m.UID)
		if uid > maxSeenUID {
			maxSeenUID = uid
		}

		marked, markErr := checkpoint.TryMarkUIDProcessed(testCfg.Host, testCfg.Username, testCfg.Inbox, uid)
		if markErr != nil {
			logx.Errorf("Test folder dry-run: could not mark UID %d as processed: %v", uid, markErr)
			continue
		}
		if !marked {
			logx.Debugf("Test folder dry-run: UID %d already processed in %s, skipping", uid, testCfg.Inbox)
			continue
		}

		var analysis provider.AnalysisResponse
		if disp == nil {
			if !providerInitialized {
				p, err = provider.New(prov.Type)
				if err != nil {
					logx.Errorf("Test folder dry-run: could not load provider: %v", err)
					return
				}
				if err = p.Init(prov.Config); err != nil {
					logx.Errorf("Test folder dry-run: could not init provider: %v", err)
					return
				}
				providerInitialized = true
			}
			analysis, err = p.Analyze(m)
		} else {
			maxRetries := 3
			if inboxCfg.MaxRetries != nil {
				maxRetries = *inboxCfg.MaxRetries
			}
			analysis, err = disp.Analyze(runCtx, m, maxRetries)
		}

		if err != nil {
			logx.Errorf("Test folder dry-run: could not analyze message #%d (%s): %v", m.UID, m.Subject, err)
			continue
		}

		logx.Infof("Test folder dry-run result: user=%s folder=%s uid=%d score=%d phishing=%t reason=%s", testCfg.Username, testCfg.Inbox, m.UID, analysis.Score, analysis.IsPhishing, analysis.Reason)
	}

	if err := checkpoint.Save(testCfg.Host, testCfg.Username, testCfg.Inbox, &checkpoint.Checkpoint{UIDValidity: currentUIDValidity, LastUID: maxSeenUID}); err != nil {
		logx.Errorf("Test folder dry-run: could not save checkpoint for %s folder %s: %v", testCfg.Username, testCfg.Inbox, err)
	}
}
