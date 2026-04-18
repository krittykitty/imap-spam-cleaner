package inbox

import (
	"context"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/internal/dispatcher"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
)

func RunAllInboxes(ctx app.Context) {
	for i, inbox := range ctx.Config.Inboxes {
		logx.Infof("Processing inbox %s", inbox.Username)
		prov, ok := ctx.Config.Providers[inbox.Provider]
		if !ok {
			logx.Errorf("Invalid provider %s for inbox %d", inbox.Provider, i)
			continue
		}
		processTestFolder(ctx, inbox, prov, nil, context.Background(), true)
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
