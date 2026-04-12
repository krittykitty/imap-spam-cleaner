package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerIsAlreadyProcessed(t *testing.T) {
	cp := &Checkpoint{UIDValidity: 1, LastUID: 100}
	mgr := NewManager("host", "user", "inbox", cp)

	if !mgr.IsAlreadyProcessed(100) {
		t.Fatalf("expected UID 100 to be already processed")
	}
	if mgr.IsAlreadyProcessed(101) {
		t.Fatalf("did not expect UID 101 to be already processed")
	}

	if err := mgr.Complete(102); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if !mgr.IsAlreadyProcessed(102) {
		t.Fatalf("expected UID 102 to be marked as completed")
	}
	if mgr.IsAlreadyProcessed(101) {
		t.Fatalf("did not expect UID 101 to be marked as completed before UID 101 completes")
	}

	if err := mgr.Complete(101); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if !mgr.IsAlreadyProcessed(101) {
		t.Fatalf("expected UID 101 to be marked as completed after Complete")
	}
	if !mgr.IsAlreadyProcessed(102) {
		t.Fatalf("expected UID 102 to remain completed after UID 101")
	}

	if mgr.LastUID() != 102 {
		t.Fatalf("expected LastUID to advance to 102, got %d", mgr.LastUID())
	}

	// Clean up generated checkpoint file.
	os.RemoveAll(filepath.Join("checkpoints"))
}
