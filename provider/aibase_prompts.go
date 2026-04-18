package provider

import (
	"bytes"
	"errors"
	"strings"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/mailclean"
)

const defaultSystemPrompt = `You are a spam classification assistant. Analyze emails objectively and return only a JSON object with the fields score, reason, and is_phishing. Only return the JSON object, no other text.`

const defaultUserPrompt = `
Analyze the following email for its spam potential.
Return your analysis as a JSON object with the following fields (order for clarity):
{
	"score": <int 0-100>,
	"is_phishing": <bool>,
	"reason": "<short explanation of why this score was given>"
}
Only return the JSON. No other text.

Recent context:
{{.Context}}

Headers:
{{.Headers}}

From: {{.From}}
To: {{.To}}
Delivered-To: {{.DeliveredTo}}
Cc: {{.Cc}}
Bcc: {{.Bcc}}
Subject: {{.Subject}}

Body (HTML converted to Markdown when available):
{{.Body}}
`

// buildUserPrompt constructs the user-facing prompt by interpolating message data.
// Prefers HTML (converted to Markdown) over plain text, applies size caps, and formats headers.
func (p *AIBase) buildUserPrompt(msg imap.Message) (string, error) {

	textBody := msg.TextBody
	htmlBody := msg.HtmlBody

	// Convert HTML body to simplified Markdown to reduce noise and token count.
	// Falls back to the raw HTML if conversion fails.
	if htmlBody != "" {
		md, err := mailclean.HTMLToSimpleMarkdown(strings.NewReader(htmlBody))
		if err != nil {
			logx.Debugf("HTML to Markdown conversion failed for message #%d (%s), using raw HTML: %v", msg.UID, msg.Subject, err)
		} else {
			htmlBody = md
		}
	}

	// Use only one body for the prompt:
	// - Prefer HTML (converted to Markdown) when available.
	// - Otherwise use plain text.
	body := textBody
	bodyKind := "text"
	if htmlBody != "" {
		body = htmlBody
		bodyKind = "HTML"
		textBody = ""
	}

	body = p.applySoftMaxsizeCap(body, uint32(msg.UID), msg.Subject, bodyKind)
	if bodyKind == "HTML" {
		htmlBody = body
	} else {
		textBody = body
		htmlBody = ""
	}

	formattedHeaders := p.formatHeaders(msg.Headers)

	var buf bytes.Buffer

	type TplVars struct {
		From        string
		To          string
		DeliveredTo string
		Cc          string
		Bcc         string
		Subject     string
		Headers     string
		TextBody    string
		HtmlBody    string
		Body        string
		Context     string
	}

	if err := p.userPrompt.Execute(&buf, TplVars{
		From:        msg.From,
		To:          msg.To,
		DeliveredTo: msg.DeliveredTo,
		Cc:          msg.Cc,
		Bcc:         msg.Bcc,
		Subject:     msg.Subject,
		Headers:     formattedHeaders,
		TextBody:    textBody,
		HtmlBody:    htmlBody,
		Body:        body,
		Context:     msg.Context,
	}); err != nil {
		return "", errors.New("user_prompt template error: " + err.Error())
	}

	return buf.String(), nil
}

// buildPrompt combines system and user prompts into a single prompt string.
func (p *AIBase) buildPrompt(msg imap.Message) (string, error) {
	userPrompt, err := p.buildUserPrompt(msg)
	if err != nil {
		return "", err
	}

	if p.systemPrompt == "" {
		return userPrompt, nil
	}

	return p.systemPrompt + "\n\n" + userPrompt, nil
}
