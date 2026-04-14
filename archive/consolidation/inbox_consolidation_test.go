//go:build archive

package inbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
	"github.com/dominicgisler/imap-spam-cleaner/storage"
)

type fakeProvider struct{}

func (f *fakeProvider) Name() string                                  { return "fake" }
func (f *fakeProvider) Init(config map[string]string) error           { return nil }
func (f *fakeProvider) ValidateConfig(config map[string]string) error { return nil }
func (f *fakeProvider) HealthCheck(config map[string]string) error    { return nil }
func (f *fakeProvider) Analyze(msg imap.Message) (provider.AnalysisResponse, error) {
	return provider.AnalysisResponse{Score: 10, Reason: "test", IsPhishing: false}, nil
}
func (f *fakeProvider) Consolidate(contextText string) (string, error) {
	return "FAKE CONSOLIDATION", nil
}

func TestRunConsolidationWithFakeProvider(t *testing.T) {
	dir := filepath.Join("testdata", "consolidation")
	_ = os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "recent.db")
	st, err := storage.NewRecent(dbPath)
	if err != nil {
		t.Fatalf("NewRecent failed: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	if err := st.UpsertMessage(storage.RecentMessage{
		UID:         1,
		From:        "alice@example.com",
		To:          "bob@example.com",
		Subject:     "Test",
		Snippet:     "snippet",
		Date:        now,
		Whitelisted: false,
	}); err != nil {
		t.Fatalf("UpsertMessage failed: %v", err)
	}

	// run consolidation with fake provider
	ctx := app.Context{Config: &app.Config{Providers: map[string]app.Provider{}}}
	if err := runConsolidation(ctx, app.Inbox{}, st, &fakeProvider{}, app.Provider{}); err != nil {
		t.Fatalf("runConsolidation failed: %v", err)
	}

	summary, err := st.GetLatestConsolidation()
	if err != nil {
		t.Fatalf("GetLatestConsolidation failed: %v", err)
	}
	if summary != "FAKE CONSOLIDATION" {
		t.Fatalf("unexpected consolidation summary: %q", summary)
	}
}

func TestShouldRunConsolidationWhenNoSummaryExists(t *testing.T) {
	dir := filepath.Join("testdata", "consolidation")
	_ = os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "recent.db")
	st, err := storage.NewRecent(dbPath)
	if err != nil {
		t.Fatalf("NewRecent failed: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	if err := st.UpsertMessage(storage.RecentMessage{
		UID:         1,
		From:        "alice@example.com",
		To:          "bob@example.com",
		Subject:     "Test",
		Snippet:     "snippet",
		Date:        now,
		Whitelisted: false,
	}); err != nil {
		t.Fatalf("UpsertMessage failed: %v", err)
	}

	if !shouldRunConsolidation(st, app.Inbox{RecentConsolidationEvery: 50}) {
		t.Fatal("expected consolidation to run when no summary exists")
	}
}
