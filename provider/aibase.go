package provider

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/mailclean"
)

var scoreRegexp = regexp.MustCompile(`(?m)(?:^|\D)([0-9]{1,3})(?:\D|$)`)

const defaultSystemPrompt = `You are a spam classification assistant. Analyze emails objectively and return only a single integer score.`

const defaultUserPrompt = `
Analyze the following email for its spam potential.
Return a spam score between 0 and 100. Only answer with the number itself, no other text.

Headers:
{{.Headers}}

From: {{.From}}
To: {{.To}}
Delivered-To: {{.DeliveredTo}}
Cc: {{.Cc}}
Bcc: {{.Bcc}}
Subject: {{.Subject}}

Email body:
{{.Body}}
`

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

	// If both formats are present, prefer cleaned HTML-only content.
	if textBody != "" && htmlBody != "" {
		textBody = ""
	}

	body := htmlBody
	if body == "" {
		body = textBody
	}

	if len(body) > p.maxsize {
		body = body[:p.maxsize]
		logx.Debugf("truncating email body for message #%d (%s)", msg.UID, msg.Subject)
	}

	type TplVars struct {
		From        string
		To          string
		DeliveredTo string
		Cc          string
		Bcc         string
		Subject     string
		Headers     string
		Body        string
	}

	var buf bytes.Buffer
	if err := p.userPrompt.Execute(&buf, TplVars{
		From:        msg.From,
		To:          msg.To,
		DeliveredTo: msg.DeliveredTo,
		Cc:          msg.Cc,
		Bcc:         msg.Bcc,
		Subject:     msg.Subject,
		Headers:     msg.Headers,
		Body:        body,
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

func parseSpamScore(resp string) (int, error) {
	matches := scoreRegexp.FindStringSubmatch(strings.TrimSpace(resp))
	if len(matches) < 2 {
		return 0, errors.New("no integer score found in response")
	}

	score, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("invalid score: %w", err)
	}
	if score < 0 || score > 100 {
		return 0, fmt.Errorf("score %d out of range", score)
	}
	return score, nil
}
