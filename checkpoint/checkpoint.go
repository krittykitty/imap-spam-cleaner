package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/dominicgisler/imap-spam-cleaner/logx"
)

const dir = "checkpoints"

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

// Checkpoint persists the IMAP UID state for incremental mail processing.
type Checkpoint struct {
	UIDValidity uint32 `json:"uid_validity"`
	LastUID     uint32 `json:"last_uid"`
}

type Manager struct {
	host      string
	username  string
	inbox     string
	baseline  uint32
	completed map[uint32]struct{}
}

func NewManager(host, username, inbox string, cp *Checkpoint) *Manager {
	baseline := uint32(0)
	if cp != nil {
		baseline = cp.LastUID
	}
	return &Manager{
		host:      host,
		username:  username,
		inbox:     inbox,
		baseline:  baseline,
		completed: make(map[uint32]struct{}),
	}
}

func (m *Manager) IsAlreadyProcessed(uid uint32) bool {
	if uid <= m.baseline {
		return true
	}
	_, ok := m.completed[uid]
	return ok
}

func (m *Manager) Complete(uid uint32) error {
	m.completed[uid] = struct{}{}
	return nil
}

func (m *Manager) LastUID() uint32 {
	maxUID := m.baseline
	for uid := range m.completed {
		if uid > maxUID {
			maxUID = uid
		}
	}
	return maxUID
}

func filePath(host, username, inbox string) string {
	sanitize := func(s string) string {
		return nonAlphanumeric.ReplaceAllString(s, "_")
	}
	name := fmt.Sprintf("%s__%s__%s.json", sanitize(host), sanitize(username), sanitize(inbox))
	return filepath.Join(dir, name)
}

func processedDirPath(host, username, inbox string) string {
	sanitize := func(s string) string {
		return nonAlphanumeric.ReplaceAllString(s, "_")
	}
	name := fmt.Sprintf("%s__%s__%s", sanitize(host), sanitize(username), sanitize(inbox))
	return filepath.Join(dir, "processed", name)
}

func processedUIDPath(host, username, inbox string, uid uint32) string {
	return filepath.Join(processedDirPath(host, username, inbox), strconv.FormatUint(uint64(uid), 10)+".uid")
}

// TryMarkUIDProcessed marks UID as processed exactly once.
// It returns true when the UID was newly marked in this call, false when it was already marked before.
func TryMarkUIDProcessed(host, username, inbox string, uid uint32) (bool, error) {
	if err := os.MkdirAll(processedDirPath(host, username, inbox), 0755); err != nil {
		return false, fmt.Errorf("failed to create processed uid directory: %w", err)
	}

	path := processedUIDPath(host, username, inbox, uid)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to mark uid as processed: %w", err)
	}
	if cerr := f.Close(); cerr != nil {
		return false, fmt.Errorf("failed to close processed uid marker: %w", cerr)
	}
	return true, nil
}

// Load returns the stored checkpoint for the given mailbox, or nil if none exists yet.
func Load(host, username, inbox string) (*Checkpoint, error) {
	path := filePath(host, username, inbox)
	logx.Debugf("Loading checkpoint from %s", path)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		logx.Debugf("Checkpoint file does not exist: %s", path)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}
	var cp Checkpoint
	if err = json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("failed to parse checkpoint: %w", err)
	}
	logx.Debugf("Loaded checkpoint from %s: UIDValidity=%d LastUID=%d", path, cp.UIDValidity, cp.LastUID)
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
