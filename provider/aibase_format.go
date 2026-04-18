package provider

import (
	"strings"

	"github.com/dominicgisler/imap-spam-cleaner/logx"
)

const softCapNumerator = 12
const softCapDenominator = 10

// formatHeaders prioritizes important trust-signal headers in the prompt.
// Displays headers in priority order (Authentication-Results, DKIM, etc.) followed by any remaining headers.
func (p *AIBase) formatHeaders(hdrs map[string]string) string {
	if len(hdrs) == 0 {
		return ""
	}

	// Define the priority order for headers to appear in the prompt.
	// Most important trust signals first.
	priorityOrder := []string{
		"Authentication-Results",
		"Return-Path",
		"Reply-To",
		"DKIM-Signature",
		"ARC-Authentication-Results",
		"Received",
		"Message-ID",
		"Sender",
		"X-Mailer",
		"User-Agent",
	}

	var lines []string

	// Add headers in priority order
	for _, name := range priorityOrder {
		if value, exists := hdrs[name]; exists && value != "" {
			// Format as "Header-Name: value"
			lines = append(lines, name+": "+value)
		}
	}

	// Add any remaining headers not in priority order
	addedHeaders := make(map[string]bool)
	for _, name := range priorityOrder {
		addedHeaders[name] = true
	}
	for name, value := range hdrs {
		if !addedHeaders[name] && value != "" {
			lines = append(lines, name+": "+value)
		}
	}

	return strings.Join(lines, "\n")
}

// applySoftMaxsizeCap enforces size limits with a soft cap (120% of maxsize).
// Allows bodies up to 120% of configured maxsize, but hard-truncates at maxsize.
func (p *AIBase) applySoftMaxsizeCap(body string, uid uint32, subject, kind string) string {
	if len(body) <= p.maxsize {
		return body
	}

	softCap := p.maxsize * softCapNumerator / softCapDenominator
	if len(body) <= softCap {
		logx.Debugf("keeping full %s body for message #%d (%s): size=%d within soft cap=%d", kind, uid, subject, len(body), softCap)
		return body
	}

	logx.Debugf("truncating %s body for message #%d (%s): size=%d exceeds soft cap=%d", kind, uid, subject, len(body), softCap)
	return body[:p.maxsize]
}
