package imap

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

type Imap struct {
	client      *imapclient.Client
	cfg         app.Inbox
	uidValidity uint32
	// newMailCh receives a signal when the server sends an EXISTS update.
	// Non-nil only when the connection was created via NewForIdle.
	newMailCh chan struct{}
}

// New creates a standard IMAP connection. Use NewForIdle when IDLE support is needed.
func New(cfg app.Inbox) (*Imap, error) {
	return newWithOptions(cfg, nil)
}

// NewForIdle creates an IMAP connection wired up to deliver new-mail
// notifications over the returned channel. Pass that connection to
// IdleUntilNew to block until new mail (or timeout/cancellation).
func NewForIdle(cfg app.Inbox) (*Imap, error) {
	ch := make(chan struct{}, 1)
	opts := &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			},
		},
	}
	im, err := newWithOptions(cfg, opts)
	if err != nil {
		return nil, err
	}
	im.newMailCh = ch
	return im, nil
}

func newWithOptions(cfg app.Inbox, opts *imapclient.Options) (*Imap, error) {

	var err error
	var mailboxes []*imap.ListData

	i := &Imap{
		cfg: cfg,
	}

	if cfg.TLS {
		i.client, err = imapclient.DialTLS(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), opts)
	} else {
		i.client, err = imapclient.DialInsecure(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), opts)
	}

	if err != nil {
		i.Close()
		return nil, fmt.Errorf("failed to dial IMAP server: %w", err)
	}

	if err = i.client.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		i.Close()
		return nil, fmt.Errorf("failed to login: %w", err)
	}

	mailboxes, err = i.client.List("", "*", nil).Collect()
	if err != nil {
		return nil, fmt.Errorf("failed to list mailboxes: %w", err)
	}

	logx.Debug("Available mailboxes:")
	for _, l := range mailboxes {
		logx.Debugf("  - %s", l.Mailbox)
	}

	mbox, err := i.client.Select(cfg.Inbox, nil).Wait()
	if err != nil {
		i.Close()
		return nil, fmt.Errorf("failed to select inbox: %w", err)
	}
	i.uidValidity = mbox.UIDValidity
	logx.Debugf("Selected inbox %s (UIDValidity=%d, NumMessages=%d)", cfg.Inbox, i.uidValidity, mbox.NumMessages)

	return i, nil
}

// GetUIDValidity returns the UIDVALIDITY value of the selected mailbox.
func (i *Imap) GetUIDValidity() uint32 {
	return i.uidValidity
}

// GetMaxUID returns the highest UID present in the mailbox, or 0 if empty.
// It fetches only UIDs (no message bodies) and is used to initialise the
// checkpoint on the very first run.
func (i *Imap) GetMaxUID() (imap.UID, error) {
	uidRes, err := i.client.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("could not search UIDs: %w", err)
	}
	uids := uidRes.AllUIDs()
	if len(uids) == 0 {
		return 0, nil
	}
	// IMAP search results are returned in ascending UID order; the last element is the maximum.
	return uids[len(uids)-1], nil
}

func (i *Imap) Close() {
	if i.client != nil {
		i.client.Logout()
		_ = i.client.Close()
	}
}

func (i *Imap) LoadMessages(sinceUID imap.UID) ([]Message, error) {

	searchCrit := &imap.SearchCriteria{}
	if i.cfg.MinAge > 0 {
		searchCrit.Before = time.Now().Add(-i.cfg.MinAge)
	}
	if i.cfg.MaxAge > 0 {
		searchCrit.Since = time.Now().Add(-i.cfg.MaxAge)
	}

	// Add UID range: only messages with UID > sinceUID.
	startUID := sinceUID + 1
	if startUID == 0 {
		// UID overflowed; no new messages possible.
		return nil, nil
	}
	var uidRangeSet imap.UIDSet
	uidRangeSet.AddRange(startUID, 0) // 0 = * (open-ended)
	searchCrit.UID = append(searchCrit.UID, uidRangeSet)

	uidRes, err := i.client.UIDSearch(searchCrit, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("could not search UIDs: %w", err)
	}

	logx.Debugf("Found %d new UIDs since UID %d", len(uidRes.AllUIDs()), sinceUID)
	if len(uidRes.AllUIDs()) == 0 {
		return nil, nil
	}

	return i.fetchAndParse(uidRes.All)
}

// LoadMessagesByUID fetches and parses messages for the given UID set.
// Age filters from the inbox configuration are applied.
func (i *Imap) LoadMessagesByUID(uidSet imap.UIDSet) ([]Message, error) {
	if len(uidSet) == 0 {
		return nil, nil
	}
	return i.fetchAndParse(uidSet)
}

// fetchAndParse fetches headers + text for numSet and returns parsed Messages.
func (i *Imap) fetchAndParse(numSet imap.NumSet) ([]Message, error) {

	var err error
	var mr *mail.Reader
	var p *mail.Part
	var messages []Message

	// Fetch headers and body text only — attachments are intentionally excluded.
	fetchOptions := &imap.FetchOptions{
		Envelope:     true,
		InternalDate: true,
		UID:          true,
		BodySection: []*imap.FetchItemBodySection{
			{
				Specifier: imap.PartSpecifierHeader,
				Peek:      true,
			},
			{
				Specifier: imap.PartSpecifierText,
				Peek:      true,
			},
		},
	}

	msgs, err := i.client.Fetch(numSet, fetchOptions).Collect()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch messages: %w", err)
	}

	for _, msg := range msgs {
		// Reconstruct a parseable RFC 5322 message from the separate header
		// and text sections returned by the partial fetch.
		var headerBytes, textBytes []byte
		for _, buf := range msg.BodySection {
			switch buf.Section.Specifier {
			case imap.PartSpecifierHeader:
				headerBytes = buf.Bytes
			case imap.PartSpecifierText:
				textBytes = buf.Bytes
			}
		}
		b := append(headerBytes, textBytes...)

		mr, err = mail.CreateReader(bytes.NewReader(b))
		if err != nil {
			logx.Warnf("failed to create message reader (msg.UID=%d): %v\n", msg.UID, err)
			continue
		}

		message := Message{
			UID:         msg.UID,
			DeliveredTo: mr.Header.Get("Delivered-To"),
			From:        mr.Header.Get("From"),
			To:          mr.Header.Get("To"),
			Cc:          mr.Header.Get("Cc"),
			Bcc:         mr.Header.Get("Bcc"),
			Subject:     msg.Envelope.Subject,
			Contents:    []string{},
			TextBody:    "",
			HtmlBody:    "",
			Raw:         b, // Raw original message bytes. Useful for traditional spam filters.
			Headers:     extractRelevantHeaders(b),
		}

		if message.Date, err = mr.Header.Date(); err != nil {
			logx.Debugf("failed to parse Date header, falling back to INTERNALDATE (msg.UID=%d): %v", msg.UID, err)
			message.Date = msg.InternalDate
		} else if message.Date.IsZero() {
			logx.Debugf("message has no Date header, falling back to INTERNALDATE (msg.UID=%d)", msg.UID)
			message.Date = msg.InternalDate
		}

		if i.cfg.MinAge > 0 && message.Date.After(time.Now().Add(-i.cfg.MinAge)) || i.cfg.MaxAge > 0 && message.Date.Before(time.Now().Add(-i.cfg.MaxAge)) {
			logx.Debugf("skipping message because date is not in range (msg.UID=%d)", msg.UID)
			continue
		}

		for {
			p, err = mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				logx.Warnf("failed to read message part (msg.UID=%d): %v\n", msg.UID, err)
				break
			}

			switch p.Header.(type) {
			case *mail.InlineHeader:
				if b, err = io.ReadAll(p.Body); err != nil {
					logx.Warnf("failed to read message body (msg.UID=%d): %v\n", msg.UID, err)
					break
				}
				message.Contents = append(message.Contents, string(b))

				mediaType := "text/plain"
				if ctype := p.Header.Get("Content-Type"); ctype != "" {
					if mt, _, err := mime.ParseMediaType(ctype); err == nil {
						mediaType = strings.ToLower(mt)
					}
				}

				switch mediaType {
				case "text/html":
					message.HtmlBody += string(b) + "\n"
				default:
					message.TextBody += string(b) + "\n"
				}
			}
		}

		messages = append(messages, message)
	}

	return messages, nil
}

func extractRelevantHeaders(raw []byte) string {
	end := bytes.Index(raw, []byte("\r\n\r\n"))
	if end < 0 {
		end = bytes.Index(raw, []byte("\n\n"))
	}
	if end < 0 {
		end = len(raw)
	}

	headers := raw[:end]
	relevant := []string{
		"Authentication-Results",
		"DKIM-Signature",
		"ARC-Authentication-Results",
		"Received",
		"Return-Path",
		"Message-ID",
		"Reply-To",
		"Sender",
	}

	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(headers))
	current := ""
	include := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if include {
				current += "\n" + line
			}
			continue
		}
		if include && current != "" {
			out = append(out, current)
		}
		include = false
		current = ""
		for _, name := range relevant {
			if strings.HasPrefix(strings.ToLower(line), strings.ToLower(name)+":") {
				include = true
				current = line
				break
			}
		}
	}
	if include && current != "" {
		out = append(out, current)
	}

	return strings.Join(out, "\n")
}

func (i *Imap) MoveMessage(uid imap.UID, mailbox string) error {
	uidSet := imap.UIDSet{}
	uidSet.AddNum(uid)
	if _, err := i.client.Move(uidSet, mailbox).Wait(); err != nil {
		return err
	}
	return nil
}

// SearchNewUIDs returns all UIDs strictly greater than sinceUID in the
// selected mailbox. Only UIDs are fetched — no message bodies.
func (i *Imap) SearchNewUIDs(sinceUID imap.UID) ([]imap.UID, error) {
	startUID := sinceUID + 1
	if startUID == 0 {
		// UID overflowed; no new messages possible.
		return nil, nil
	}
	var uidSet imap.UIDSet
	uidSet.AddRange(startUID, 0) // 0 = * (open-ended)
	crit := &imap.SearchCriteria{
		UID: []imap.UIDSet{uidSet},
	}
	res, err := i.client.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("could not search UIDs: %w", err)
	}
	return res.AllUIDs(), nil
}

// IdleUntilNew puts the connection into IMAP IDLE mode and blocks until one
// of the following occurs:
//   - the server signals a new message (EXISTS response),
//   - the provided context is cancelled,
//   - the idleTimeout elapses (caller should re-issue IDLE).
//
// The connection must have been created with NewForIdle.
// A non-nil error is returned only for unexpected connection failures.
// context.Canceled or context.DeadlineExceeded is returned when ctx is done.
func (i *Imap) IdleUntilNew(ctx context.Context, idleTimeout time.Duration) error {
	if i.newMailCh == nil {
		return fmt.Errorf("IdleUntilNew requires a connection created with NewForIdle")
	}

	idleCmd, err := i.client.Idle()
	if err != nil {
		return fmt.Errorf("could not start IDLE: %w", err)
	}

	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()

	var stopErr error
	select {
	case <-i.newMailCh:
		// New mail arrived.
	case <-timer.C:
		// Idle timeout — caller will re-issue IDLE.
	case <-ctx.Done():
		stopErr = ctx.Err()
	}

	if err := idleCmd.Close(); err != nil {
		return fmt.Errorf("could not stop IDLE: %w", err)
	}
	if err := idleCmd.Wait(); err != nil && stopErr == nil {
		return fmt.Errorf("IDLE error: %w", err)
	}
	return stopErr
}
