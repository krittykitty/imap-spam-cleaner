//go:build archive

package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ptrFloat64(value float64) *float64 {
	return &value
}

func TestRecentStoreUpsertAndQuery(t *testing.T) {
	dir := filepath.Join("testdata", "recent")
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "recent.db")
	st, err := NewRecent(dbPath)
	if err != nil {
		t.Fatalf("NewRecent failed: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	msg := RecentMessage{
		UID:         1,
		From:        "alice@example.com",
		To:          "bob@example.com",
		Subject:     "Hello",
		Snippet:     "Hello Bob, this is a test email.",
		Date:        now,
		SpamScore:   ptrFloat64(42.5),
		LLMReason:   "Contains unexpected link",
		Whitelisted: false,
	}

	if err := st.UpsertMessage(msg); err != nil {
		t.Fatalf("UpsertMessage failed: %v", err)
	}

	ctx, err := st.GetConsolidatedContext(1, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("GetConsolidatedContext failed: %v", err)
	}
	if !strings.Contains(ctx, "Recent messages:") {
		t.Fatalf("expected consolidated context to include recent messages, got %q", ctx)
	}

	summary, err := st.GetLatestConsolidation()
	if err != nil {
		t.Fatalf("GetLatestConsolidation failed: %v", err)
	}
	if summary != "" {
		t.Fatalf("expected no consolidation yet, got %q", summary)
	}

	old := now.Add(-90 * 24 * time.Hour)
	oldMsg := RecentMessage{
		UID:         2,
		From:        "carol@example.com",
		To:          "bob@example.com",
		Subject:     "Old email",
		Snippet:     "This is an older email.",
		Date:        old,
		Whitelisted: false,
	}
	if err := st.UpsertMessage(oldMsg); err != nil {
		t.Fatalf("UpsertMessage failed for old message: %v", err)
	}

	cutoff := now.Add(-60 * 24 * time.Hour)
	if err := st.PruneOlderThan(cutoff); err != nil {
		t.Fatalf("PruneOlderThan failed: %v", err)
	}

	ctx, err = st.GetConsolidatedContext(10, 365*24*time.Hour)
	if err != nil {
		t.Fatalf("GetConsolidatedContext after prune failed: %v", err)
	}
	if strings.Contains(ctx, "Old email") {
		t.Fatal("expected old message to be pruned from consolidated context")
	}
}

func TestRecentStoreConsolidationStatePersistence(t *testing.T) {
	dir := filepath.Join("testdata", "recent_state")
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "recent.db")
	st, err := NewRecent(dbPath)
	if err != nil {
		t.Fatalf("NewRecent failed: %v", err)
	}
	defer st.Close()

	done, err := st.IsInitialPopulationDone()
	if err != nil {
		t.Fatalf("IsInitialPopulationDone failed: %v", err)
	}
	if done {
		t.Fatal("expected initial population to be false by default")
	}

	if err := st.MarkInitialPopulationDone(); err != nil {
		t.Fatalf("MarkInitialPopulationDone failed: %v", err)
	}
	done, err = st.IsInitialPopulationDone()
	if err != nil {
		t.Fatalf("IsInitialPopulationDone failed after mark: %v", err)
	}
	if !done {
		t.Fatal("expected initial population to be true after mark")
	}

	pending, err := st.GetConsolidationPendingCount()
	if err != nil {
		t.Fatalf("GetConsolidationPendingCount failed: %v", err)
	}
	if pending != 0 {
		t.Fatalf("expected pending=0, got %d", pending)
	}

	pending, err = st.AddConsolidationPending(7)
	if err != nil {
		t.Fatalf("AddConsolidationPending failed: %v", err)
	}
	if pending != 7 {
		t.Fatalf("expected pending=7, got %d", pending)
	}

	if err := st.SaveConsolidation("summary"); err != nil {
		t.Fatalf("SaveConsolidation failed: %v", err)
	}

	lastRun, err := st.GetConsolidationLastRun()
	if err != nil {
		t.Fatalf("GetConsolidationLastRun failed: %v", err)
	}
	if lastRun.IsZero() {
		t.Fatal("expected non-zero consolidation last run")
	}

	if err := st.ResetConsolidationPending(); err != nil {
		t.Fatalf("ResetConsolidationPending failed: %v", err)
	}
	pending, err = st.GetConsolidationPendingCount()
	if err != nil {
		t.Fatalf("GetConsolidationPendingCount failed after reset: %v", err)
	}
	if pending != 0 {
		t.Fatalf("expected pending=0 after reset, got %d", pending)
	}
}
