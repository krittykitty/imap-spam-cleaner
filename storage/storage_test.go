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
