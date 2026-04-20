package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageAddsAndFindsContacts(t *testing.T) {
	dir := filepath.Join("testdata", "storage")
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "contacts.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New storage failed: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	if err := st.BatchAddContacts([]string{"Alice <alice@example.com>", "bob@example.com"}, now); err != nil {
		t.Fatalf("BatchAddContacts failed: %v", err)
	}

	found, err := st.HasContact("alice@example.com")
	if err != nil {
		t.Fatalf("HasContact failed: %v", err)
	}
	if !found {
		t.Fatal("expected alice@example.com to be found")
	}

	found, err = st.HasContact("ALICE@EXAMPLE.COM")
	if err != nil {
		t.Fatalf("HasContact failed: %v", err)
	}
	if !found {
		t.Fatal("expected ALICE@EXAMPLE.COM to be found after normalization")
	}

	found, err = st.HasContact("charlie@example.com")
	if err != nil {
		t.Fatalf("HasContact failed: %v", err)
	}
	if found {
		t.Fatal("expected charlie@example.com to be missing")
	}

	count, err := st.ContactCount()
	if err != nil {
		t.Fatalf("ContactCount failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 contacts after initial insert, got %d", count)
	}

	old := now.Add(-24 * time.Hour)
	if err := st.BatchAddContacts([]string{"old@example.com"}, old); err != nil {
		t.Fatalf("BatchAddContacts failed: %v", err)
	}
	cutoff := now.Add(-12 * time.Hour)
	if err := st.PruneOlderThan(cutoff); err != nil {
		t.Fatalf("PruneOlderThan failed: %v", err)
	}
	found, err = st.HasContact("old@example.com")
	if err != nil {
		t.Fatalf("HasContact failed: %v", err)
	}
	if found {
		t.Fatal("expected old@example.com to be pruned")
	}
}

func TestStorageMergeContactsFromFile(t *testing.T) {
	dir := filepath.Join("testdata", "storage", "merge")
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	sourcePath := filepath.Join(dir, "legacy.db")
	source, err := New(sourcePath)
	if err != nil {
		t.Fatalf("New source storage failed: %v", err)
	}

	if err := source.BatchAddContacts([]string{"Legacy One <legacy1@example.com>", "legacy2@example.com"}, time.Now().UTC()); err != nil {
		source.Close()
		t.Fatalf("source BatchAddContacts failed: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("source Close failed: %v", err)
	}

	targetPath := filepath.Join(dir, "target.db")
	target, err := New(targetPath)
	if err != nil {
		t.Fatalf("New target storage failed: %v", err)
	}
	defer target.Close()

	if err := target.BatchAddContacts([]string{"existing@example.com"}, time.Now().UTC()); err != nil {
		t.Fatalf("target BatchAddContacts failed: %v", err)
	}

	imported, err := target.MergeContactsFromFile(sourcePath)
	if err != nil {
		t.Fatalf("MergeContactsFromFile failed: %v", err)
	}
	if imported != 2 {
		t.Fatalf("expected to import 2 contacts, got %d", imported)
	}

	for _, email := range []string{"existing@example.com", "legacy1@example.com", "legacy2@example.com"} {
		found, err := target.HasContact(email)
		if err != nil {
			t.Fatalf("HasContact(%s) failed: %v", email, err)
		}
		if !found {
			t.Fatalf("expected %s to exist after merge", email)
		}
	}

	count, err := target.ContactCount()
	if err != nil {
		t.Fatalf("ContactCount failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 contacts after merge, got %d", count)
	}
}

func TestFeedbackWhitelistTTLAndScope(t *testing.T) {
	dir := filepath.Join("testdata", "storage", "feedback")
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	st, err := New(filepath.Join(dir, "feedback.db"))
	if err != nil {
		t.Fatalf("New storage failed: %v", err)
	}
	defer st.Close()

	if err := st.AddFeedbackWhitelist("Inbox", "Alice <alice@example.com>", "moved_back_from_spam", 24*time.Hour); err != nil {
		t.Fatalf("AddFeedbackWhitelist failed: %v", err)
	}

	now := time.Now().UTC()
	has, err := st.HasFeedbackWhitelist("INBOX", "alice@example.com", now)
	if err != nil {
		t.Fatalf("HasFeedbackWhitelist failed: %v", err)
	}
	if !has {
		t.Fatal("expected sender to be whitelisted in same inbox scope")
	}

	has, err = st.HasFeedbackWhitelist("OtherInbox", "alice@example.com", now)
	if err != nil {
		t.Fatalf("HasFeedbackWhitelist failed: %v", err)
	}
	if has {
		t.Fatal("expected sender to be missing in different inbox scope")
	}

	has, err = st.HasFeedbackWhitelist("Inbox", "alice@example.com", now.Add(25*time.Hour))
	if err != nil {
		t.Fatalf("HasFeedbackWhitelist after expiry failed: %v", err)
	}
	if has {
		t.Fatal("expected sender to expire from feedback whitelist")
	}

	pruned, err := st.PruneExpiredFeedbackWhitelist(now.Add(25 * time.Hour))
	if err != nil {
		t.Fatalf("PruneExpiredFeedbackWhitelist failed: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned feedback entry, got %d", pruned)
	}
}

func TestSpamMoveMarkerConsumeAndPrune(t *testing.T) {
	dir := filepath.Join("testdata", "storage", "spam_move_markers")
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	st, err := New(filepath.Join(dir, "markers.db"))
	if err != nil {
		t.Fatalf("New storage failed: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	if err := st.AddSpamMoveMarker("Inbox", "<msg-1@example.com>", "Alice <alice@example.com>", now); err != nil {
		t.Fatalf("AddSpamMoveMarker failed: %v", err)
	}

	sender, found, err := st.ConsumeSpamMoveMarker("INBOX", "msg-1@example.com")
	if err != nil {
		t.Fatalf("ConsumeSpamMoveMarker failed: %v", err)
	}
	if !found {
		t.Fatal("expected spam move marker to be found")
	}
	if sender != "alice@example.com" {
		t.Fatalf("unexpected sender from marker: got %q", sender)
	}

	_, found, err = st.ConsumeSpamMoveMarker("Inbox", "msg-1@example.com")
	if err != nil {
		t.Fatalf("ConsumeSpamMoveMarker second read failed: %v", err)
	}
	if found {
		t.Fatal("expected marker to be deleted after consume")
	}

	if err := st.AddSpamMoveMarker("Inbox", "msg-old@example.com", "old@example.com", now.Add(-200*24*time.Hour)); err != nil {
		t.Fatalf("AddSpamMoveMarker old failed: %v", err)
	}
	if err := st.AddSpamMoveMarker("Inbox", "msg-new@example.com", "new@example.com", now); err != nil {
		t.Fatalf("AddSpamMoveMarker new failed: %v", err)
	}

	pruned, err := st.PruneSpamMoveMarkersOlderThan(now.Add(-180 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("PruneSpamMoveMarkersOlderThan failed: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned marker, got %d", pruned)
	}

	_, found, err = st.ConsumeSpamMoveMarker("Inbox", "msg-old@example.com")
	if err != nil {
		t.Fatalf("ConsumeSpamMoveMarker old failed: %v", err)
	}
	if found {
		t.Fatal("expected old marker to be pruned")
	}

	_, found, err = st.ConsumeSpamMoveMarker("Inbox", "msg-new@example.com")
	if err != nil {
		t.Fatalf("ConsumeSpamMoveMarker new failed: %v", err)
	}
	if !found {
		t.Fatal("expected new marker to remain after pruning")
	}
}
