package inbox

import (
	"fmt"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/checkpoint"
	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/storage"
	goimap "github.com/emersion/go-imap/v2"
)

func syncSentFolder(ctx app.Context, inboxCfg app.Inbox) error {
	if !inboxCfg.EnableSentWhitelist {
		return nil
	}

	st, ok := ctx.Storages[storage.DBPath(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox)]
	if !ok || st == nil {
		return fmt.Errorf("sent-folder storage unavailable for inbox %s", inboxCfg.Username)
	}

	sentCfg := inboxCfg
	sentCfg.Inbox = inboxCfg.SentFolder
	sentCfg.MinAge = 0
	sentCfg.MaxAge = inboxCfg.SentFolderMaxAge
	if sentCfg.MaxAge == 0 {
		sentCfg.MaxAge = 2160 * time.Hour
	}

	cp, err := checkpoint.Load(inboxCfg.Host, inboxCfg.Username, inboxCfg.SentFolder)
	if err != nil {
		return fmt.Errorf("could not load sent-folder checkpoint: %w", err)
	}

	im, err := imap.New(sentCfg)
	if err != nil {
		return fmt.Errorf("could not open sent folder %s: %w", inboxCfg.SentFolder, err)
	}
	defer im.Close()

	currentUIDValidity := im.GetUIDValidity()
	if cp == nil {
		logx.Infof("Initial sent-folder sync for %s (%s)", inboxCfg.Username, inboxCfg.SentFolder)
		cp = &checkpoint.Checkpoint{UIDValidity: currentUIDValidity, LastUID: 0}
	} else if cp.UIDValidity != currentUIDValidity {
		logx.Warnf("Sent folder UIDVALIDITY changed for %s (%s): scanning full folder", inboxCfg.Username, inboxCfg.SentFolder)
		cp = &checkpoint.Checkpoint{UIDValidity: currentUIDValidity, LastUID: 0}
	}

	sinceUID := goimap.UID(cp.LastUID)
	msgs, err := im.LoadHeaders(sinceUID)
	if err != nil {
		return fmt.Errorf("could not load sent folder messages: %w", err)
	}

	logx.Infof("Loaded %d sent headers from sent folder %s since UID %d", len(msgs), inboxCfg.SentFolder, sinceUID)

	recipients := make(map[string]struct{})
	newestUID := goimap.UID(cp.LastUID)
	for _, m := range msgs {
		if m.UID > newestUID {
			newestUID = m.UID
		}
		for _, email := range extractRecipientEmails(m) {
			recipients[email] = struct{}{}
		}
	}

	if len(recipients) > 0 {
		contactList := make([]string, 0, len(recipients))
		for email := range recipients {
			contactList = append(contactList, email)
		}
		if err := st.BatchAddContacts(contactList, time.Now()); err != nil {
			return fmt.Errorf("could not save sent contacts: %w", err)
		}
	}

	if inboxCfg.SentFolderMaxAge > 0 {
		if err := st.PruneOlderThan(time.Now().Add(-inboxCfg.SentFolderMaxAge)); err != nil {
			return fmt.Errorf("could not prune sent contacts: %w", err)
		}
	}

	if len(msgs) == 0 {
		maxUID, err := im.GetMaxUID()
		if err != nil {
			return fmt.Errorf("could not get maximum UID for sent folder: %w", err)
		}
		newestUID = maxUID
	}

	if err := checkpoint.Save(inboxCfg.Host, inboxCfg.Username, inboxCfg.SentFolder, &checkpoint.Checkpoint{
		UIDValidity: currentUIDValidity,
		LastUID:     uint32(newestUID),
	}); err != nil {
		return fmt.Errorf("could not save sent-folder checkpoint: %w", err)
	}

	logx.Infof("Sent-folder sync complete for %s (%s); known contacts=%d", inboxCfg.Username, inboxCfg.SentFolder, len(recipients))
	return nil
}

func extractRecipientEmails(message imap.Message) []string {
	emails := make([]string, 0, 2)
	for _, email := range storage.ParseAddressList(message.To) {
		emails = append(emails, email)
	}
	return emails
}
