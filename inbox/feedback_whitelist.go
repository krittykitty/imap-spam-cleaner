package inbox

import (
	"strings"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/provider"
	"github.com/dominicgisler/imap-spam-cleaner/storage"
)

const (
	feedbackWhitelistTTL = 90 * 24 * time.Hour
	spamMoveMarkerMaxAge = 180 * 24 * time.Hour
)

func firstSenderEmail(from string) string {
	emails := storage.ParseAddressList(from)
	if len(emails) == 0 {
		return ""
	}
	return emails[0]
}

func shouldAutoWhitelistNonSpam(analysis provider.AnalysisResponse, minScore int) bool {
	if analysis.IsSpam || analysis.IsPhishing {
		return false
	}
	return analysis.Score < minScore
}

func messageIDForTracking(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	messageID := strings.TrimSpace(headers["Message-ID"])
	if messageID == "" {
		messageID = strings.TrimSpace(headers["Message-Id"])
	}
	if messageID == "" {
		return ""
	}
	messageID = strings.Trim(messageID, "<>")
	return strings.ToLower(strings.TrimSpace(messageID))
}
