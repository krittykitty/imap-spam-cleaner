package storage

import (
	"fmt"
	"path/filepath"
)

// RecentDBPath is kept for backward compatibility but recent message storage
// is currently disabled in the active workflow.
func RecentDBPath(host, username, inbox string) string {
	filename := fmt.Sprintf("recent_messages__%s__%s__%s.db", sanitizeFileName(host), sanitizeFileName(username), sanitizeFileName(inbox))
	return filepath.Join("storage", filename)
}
