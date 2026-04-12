package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

const dir = "checkpoints"

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

// Checkpoint persists the IMAP UID state for incremental mail processing.
type Checkpoint struct {
	UIDValidity uint32 `json:"uid_validity"`
	LastUID     uint32 `json:"last_uid"`
}

func filePath(host, username, inbox string) string {
	sanitize := func(s string) string {
		return nonAlphanumeric.ReplaceAllString(s, "_")
	}
	name := fmt.Sprintf("%s__%s__%s.json", sanitize(host), sanitize(username), sanitize(inbox))
	return filepath.Join(dir, name)
}

// Load returns the stored checkpoint for the given mailbox, or nil if none exists yet.
func Load(host, username, inbox string) (*Checkpoint, error) {
	path := filePath(host, username, inbox)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}
	var cp Checkpoint
	if err = json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("failed to parse checkpoint: %w", err)
	}
	return &cp, nil
}

// Save writes the checkpoint for the given mailbox to persistent storage.
func Save(host, username, inbox string, cp *Checkpoint) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}
	path := filePath(host, username, inbox)
	data, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}
	if err = os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write checkpoint: %w", err)
	}
	return nil
}

// Manager tracks asynchronous checkpoint advancement for a single mailbox.
//
// Because IDLE-mode jobs are processed concurrently and may complete
// out-of-order, Manager keeps a "completed" set of UIDs that are done but
// cannot yet be persisted (there is a gap between lastUID and the lowest
// completed UID). After each call to Complete, it sweeps contiguously from
// lastUID+1 upward, advances lastUID as far as possible, and persists.
//
// This guarantees monotonic progress: a crash can only cause re-processing of
// the gap between lastUID and the lowest unprocessed UID — never a skip.
type Manager struct {
	mu        sync.Mutex
	lastUID   uint32
	uidValid  uint32
	completed map[uint32]struct{}
	host      string
	username  string
	inbox     string
}

// NewManager creates a Manager seeded from an existing Checkpoint.
// cp must not be nil.
func NewManager(host, username, inbox string, cp *Checkpoint) *Manager {
	return &Manager{
		lastUID:   cp.LastUID,
		uidValid:  cp.UIDValidity,
		completed: make(map[uint32]struct{}),
		host:      host,
		username:  username,
		inbox:     inbox,
	}
}

// LastUID returns the most recently persisted LastUID value.
func (m *Manager) LastUID() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastUID
}

// Complete marks uid as successfully processed, advances the checkpoint as far
// as possible, and persists it.  It is safe to call from multiple goroutines.
func (m *Manager) Complete(uid uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.completed[uid] = struct{}{}

	// Sweep contiguously from lastUID+1 upward.
	for {
		next := m.lastUID + 1
		if _, ok := m.completed[next]; !ok {
			break
		}
		delete(m.completed, next)
		m.lastUID = next
	}

	return Save(m.host, m.username, m.inbox, &Checkpoint{
		UIDValidity: m.uidValid,
		LastUID:     m.lastUID,
	})
}

