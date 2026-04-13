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

// textBodyDivisor controls the plain-text share of the LLM prompt budget when
// both text and HTML bodies are present: plain-text gets 1/textBodyDivisor of
// maxsize; the rest goes to the HTML-derived Markdown, reflecting that spam
// signals tend to be denser in the HTML part.
const textBodyDivisor = 4

const defaultSystemPrompt = `You are a spam classification assistant. Analyze emails objectively and return only a JSON object with the fields score, reason, and is_phishing. Only return the JSON object, no other text.`

const defaultConsolidationPrompt = `
Summarize the recent email activity in five sentences.
Include the normal senders, subjects, and any spam-related signals.
Use the consolidated context and the recent message metadata to produce a short summary.
Only return the summary text and no JSON.

{{.Context}}
`

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

Text body:
{{.TextBody}}

HTML body (converted to Markdown):
{{.HtmlBody}}
`

type AnalysisResponse struct {
	Score      int    `json:"score"`
	Reason     string `json:"reason"`
	IsPhishing bool   `json:"is_phishing"`
}

type AIBase struct {
	model               string
	maxsize             int
	systemPrompt        string
	userPrompt          *template.Template
	consolidationPrompt *template.Template
	temperature         *float32
	topP                *float32
	maxTokens           *int32
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

	consolidationPromptStr := defaultConsolidationPrompt
	if config["consolidation_prompt"] != "" {
		consolidationPromptStr = config["consolidation_prompt"]
	}
	p.consolidationPrompt, err = template.New("consolidation_prompt").Parse(consolidationPromptStr)
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

	if s := config["max_tokens"]; s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil || n < 1 {
			return errors.New("max_tokens must be a positive integer")
		}
		v := int32(n)
		p.maxTokens = &v
	}

	return nil
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

	// Apply size limits. When both bodies are present, allocate 1/4 of the
	// budget to plain-text and 3/4 to the HTML-derived Markdown — spam
	// signals tend to be denser in the HTML part.
	if textBody != "" && htmlBody != "" {
		textLimit := p.maxsize / textBodyDivisor
		htmlLimit := p.maxsize - textLimit
		if len(textBody) > textLimit {
			textBody = textBody[:textLimit]
			logx.Debugf("truncating text body for message #%d (%s)", msg.UID, msg.Subject)
		}
		if len(htmlBody) > htmlLimit {
			htmlBody = htmlBody[:htmlLimit]
			logx.Debugf("truncating HTML body for message #%d (%s)", msg.UID, msg.Subject)
		}
	} else {
		if len(textBody) > p.maxsize {
			textBody = textBody[:p.maxsize]
			logx.Debugf("truncating text body for message #%d (%s)", msg.UID, msg.Subject)
		}
		if len(htmlBody) > p.maxsize {
			htmlBody = htmlBody[:p.maxsize]
			logx.Debugf("truncating HTML body for message #%d (%s)", msg.UID, msg.Subject)
		}
	}

	body := htmlBody
	if body == "" {
		body = textBody
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

func (p *AIBase) buildConsolidationPrompt(contextText string) (string, error) {
	tpl := p.consolidationPrompt
	if tpl == nil {
		var err error
		tpl, err = template.New("consolidation_prompt").Parse(defaultConsolidationPrompt)
		if err != nil {
			return "", err
		}
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, struct{ Context string }{Context: contextText}); err != nil {
		return "", err
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
