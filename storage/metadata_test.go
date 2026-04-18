package storage

import (
	path "path/filepath"
	"testing"
)

func TestMailboxMetadataSetGet(t *testing.T) {
	dir := t.TempDir()
	dbPath := path.Join(dir, "test_sent.db")

	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer st.Close()

	// ensure empty initially
	v, err := st.GetMailbox("spam")
	if err != nil {
		t.Fatalf("GetMailbox() error: %v", err)
	}
	if v != "" {
		t.Fatalf("expected empty mailbox, got %q", v)
	}

	if err := st.SetMailbox("spam", "INBOX.Spam"); err != nil {
		t.Fatalf("SetMailbox() error: %v", err)
	}

	v2, err := st.GetMailbox("spam")
	if err != nil {
		t.Fatalf("GetMailbox() error: %v", err)
	}
	if v2 != "INBOX.Spam" {
		t.Fatalf("expected INBOX.Spam, got %q", v2)
	}

	// reopen DB to confirm persistence
	st2, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() reopen error: %v", err)
	}
	defer st2.Close()

	v3, err := st2.GetMailbox("spam")
	if err != nil {
		t.Fatalf("GetMailbox() error after reopen: %v", err)
	}
	if v3 != "INBOX.Spam" {
		t.Fatalf("expected INBOX.Spam after reopen, got %q", v3)
	}
}
