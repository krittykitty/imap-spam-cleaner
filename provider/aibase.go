package provider

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"text/template"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/mailclean"
)

const minMaxTokens = int32(500)
const softCapNumerator = 12
const softCapDenominator = 10

const defaultSystemPrompt = `You are a spam classification assistant. Analyze emails objectively and return only a JSON object with the fields score, reason, and is_phishing. Only return the JSON object, no other text.`

const defaultUserPrompt = `
Analyze the following email for its spam potential.
Return your analysis as a JSON object with the following fields:
{
  "score": <int 0-100>,
  "reason": "<short explanation of why this score was given>",
  "is_phishing": <bool>
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

type AnalysisResponse struct {
	Score      int    `json:"score"`
	Reason     string `json:"reason"`
	IsPhishing bool   `json:"is_phishing"`
}

type AIBase struct {
	model        string
	maxsize      int
	systemPrompt string
	userPrompt   *template.Template
	temperature  *float32
	topP         *float32
	maxTokens    *int32
}

func (p *AIBase) ValidateConfig(config map[string]string) error {

	if config["model"] == "" {
		return errors.New("ai model is required")
	}
	p.model = config["model"]

	n, err := strconv.Atoi(config["maxsize"])
	if err != nil || n < 1 {
		return errors.New("maxsize must be a positive integer")
	}
	p.maxsize = n

	p.systemPrompt = defaultSystemPrompt
	if config["system_prompt"] != "" {
		p.systemPrompt = config["system_prompt"]
	}

	// "user_prompt" is the canonical key; "prompt" is kept for backward compatibility.
	userPromptStr := defaultUserPrompt
	if config["user_prompt"] != "" {
		userPromptStr = config["user_prompt"]
	} else if config["prompt"] != "" {
		userPromptStr = config["prompt"]
	}

	p.userPrompt, err = template.New("user_prompt").Parse(userPromptStr)
	if err != nil {
		return err
	}

	if s := config["temperature"]; s != "" {
		f, err := strconv.ParseFloat(s, 32)
		if err != nil {
			return errors.New("temperature must be a float")
		}
		v := float32(f)
		p.temperature = &v
	}

	if s := config["top_p"]; s != "" {
		f, err := strconv.ParseFloat(s, 32)
		if err != nil {
			return errors.New("top_p must be a float")
		}
		v := float32(f)
		p.topP = &v
	}

	v := minMaxTokens
	p.maxTokens = &v
	if s := config["max_tokens"]; s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil || n < 1 {
			return errors.New("max_tokens must be a positive integer")
		}
		if n < int64(minMaxTokens) {
			logx.Warnf("Configured max_tokens=%d is too low; enforcing minimum of %d", n, minMaxTokens)
			n = int64(minMaxTokens)
		}
		vv := int32(n)
		p.maxTokens = &vv
	}

	return nil
}

func (p *AIBase) effectiveMaxTokens() int32 {
	if p == nil || p.maxTokens == nil || *p.maxTokens < minMaxTokens {
		return minMaxTokens
	}
	return *p.maxTokens
}

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
