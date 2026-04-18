package inbox

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
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

		processTestFolder(ctx, inbox, prov, dispatchers[inbox.Provider], shutdownCtx, true)

		if inbox.EnableIdle {
			logx.Infof("Skipping cron for idle inbox %s", inbox.Username)
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
			// pass a dispatcher for the provider if one exists (may be nil)
			go StartIdle(shutdownCtx, ctx, inbox, prov, dispatchers[inbox.Provider])
			if inbox.TestFolder != "" && inbox.TestFolder != inbox.Inbox {
				logx.Infof("Starting additional IDLE watcher for test folder %s (%s)", inbox.TestFolder, inbox.Username)
				testInboxCfg := inbox
				testInboxCfg.Inbox = inbox.TestFolder
				go StartIdleTestFolder(shutdownCtx, ctx, testInboxCfg, prov, dispatchers[inbox.Provider])
				idleCount++
			}
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
