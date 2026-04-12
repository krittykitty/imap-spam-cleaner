package dispatcher

import (
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/imap"
	goimap "github.com/emersion/go-imap/v2"
)

// Job is a unit of work that asks a provider to analyse a single message.
type Job struct {
	// InboxCfg is the inbox that produced this message.
	InboxCfg app.Inbox
	// ProviderCfg is the provider configuration to use for analysis.
	ProviderCfg app.Provider
	// ProviderName is the key in Config.Providers used to look up ProviderCfg.
	ProviderName string
	// Message is the already-fetched, fully-parsed mail message.
	Message imap.Message
	// Retries is incremented each time the job is re-queued after a transient failure.
	Retries int
	// EnqueuedAt is the wall-clock time the job was first submitted.
	EnqueuedAt time.Time
	// ResultCh receives exactly one Result when the job has finished
	// (successfully or after exhausting retries).
	ResultCh chan<- Result
}

// Result is the outcome of a processed Job, sent back to the inbox controller.
type Result struct {
	// UID of the message that was processed.
	UID goimap.UID
	// Success is true when the provider returned a score without error and
	// any required move operation also succeeded.
	Success bool
	// SpamScore is the score returned by the provider (0–100).
	// Only meaningful when Success is true.
	SpamScore int
	// ShouldMove is true when SpamScore >= InboxCfg.MinScore and the
	// message should be moved to the spam folder.
	ShouldMove bool
	// Err holds the last error encountered (nil on success).
	Err error
}
