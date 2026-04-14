package storage

import (
	"fmt"
	"path/filepath"
	"strings"
)

// sanitizeFileName is shared with sent-contact DB path helpers.
var sanitizeFileName = func(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

// RecentDBPath is kept for backward compatibility but recent message storage
// is currently disabled in the active workflow.
func RecentDBPath(host, username, inbox string) string {
	filename := fmt.Sprintf("recent_messages__%s__%s__%s.db", sanitizeFileName(host), sanitizeFileName(username), sanitizeFileName(inbox))
	return filepath.Join("storage", filename)
}
