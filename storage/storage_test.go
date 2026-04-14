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
