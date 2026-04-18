package provider

import (
	"errors"
	"strconv"
	"text/template"

	"github.com/dominicgisler/imap-spam-cleaner/logx"
)

const minMaxTokens = int32(500)

// ValidateConfig validates and populates AIBase configuration from config map.
// Enforces required fields (model, maxsize) and applies defaults for optional fields.
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

	// Parse user prompt with backward compatibility for "prompt" key
	userPromptStr := defaultUserPrompt
	if config["user_prompt"] != "" {
		userPromptStr = config["user_prompt"]
	} else if config["prompt"] != "" {
		userPromptStr = config["prompt"]
	}

	var parseErr error
	p.userPrompt, parseErr = template.New("user_prompt").Parse(userPromptStr)
	if parseErr != nil {
		return parseErr
	}

	// Parse optional float parameters
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

	// Parse max_tokens with minimum enforcement
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
