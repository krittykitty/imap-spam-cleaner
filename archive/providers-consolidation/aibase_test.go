//go:build archive

package provider

import (
	"strings"
	"testing"
)

func TestBuildConsolidationPromptDefaultIncludesPreviousConsolidation(t *testing.T) {
	p := &AIBase{}

	prompt, err := p.buildConsolidationPrompt(ConsolidationPromptVars{
		PreviousConsolidation: "old summary",
		LatestSenders:         "alice@example.com, bob@example.com",
		Messages: []ConsolidationMessage{
			{
				From:      "alice@example.com",
				To:        "bob@example.com",
				Subject:   "Hello",
				SpamScore: "85",
				LLMReason: "suspicious link",
			},
		},
	})
	if err != nil {
		t.Fatalf("buildConsolidationPrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "old summary") {
		t.Fatalf("expected previous consolidation in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "alice@example.com") {
		t.Fatalf("expected latest senders in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "suspicious link") {
		t.Fatalf("expected message reason in prompt, got %q", prompt)
	}
}

func TestValidateConfigParsesConsolidationPromptAandB(t *testing.T) {
	p := &AIBase{}
	config := map[string]string{
		"model":                       "test-model",
		"maxsize":                     "1000",
		"system_prompt":               "A system prompt",
		"user_prompt":                 "A user prompt with {{.Context}}",
		"consolidation_system_prompt": "B system prompt",
		"consolidation_user_prompt":   "B user prompt with {{.PreviousConsolidation}}",
	}

	if err := p.ValidateConfig(config); err != nil {
		t.Fatalf("ValidateConfig failed: %v", err)
	}

	prompt, err := p.buildConsolidationPrompt(ConsolidationPromptVars{PreviousConsolidation: "old"})
	if err != nil {
		t.Fatalf("buildConsolidationPrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "B user prompt") {
		t.Fatalf("expected consolidation user prompt to be used, got %q", prompt)
	}
	if p.consolidationSystemPrompt != "B system prompt" {
		t.Fatalf("expected consolidation system prompt to be stored, got %q", p.consolidationSystemPrompt)
	}
}

func TestValidateConfigMaxTokensDefaultAndMinimum(t *testing.T) {
	t.Run("defaults to minimum when not set", func(t *testing.T) {
		p := &AIBase{}
		cfg := map[string]string{
			"model":   "test-model",
			"maxsize": "1000",
		}
		if err := p.ValidateConfig(cfg); err != nil {
			t.Fatalf("ValidateConfig failed: %v", err)
		}
		if p.maxTokens == nil {
			t.Fatal("expected maxTokens to be initialized")
		}
		if *p.maxTokens != minMaxTokens {
			t.Fatalf("expected default maxTokens=%d, got %d", minMaxTokens, *p.maxTokens)
		}
	})

	t.Run("enforces minimum when configured too low", func(t *testing.T) {
		p := &AIBase{}
		cfg := map[string]string{
			"model":      "test-model",
			"maxsize":    "1000",
			"max_tokens": "8",
		}
		if err := p.ValidateConfig(cfg); err != nil {
			t.Fatalf("ValidateConfig failed: %v", err)
		}
		if p.maxTokens == nil {
			t.Fatal("expected maxTokens to be initialized")
		}
		if *p.maxTokens != minMaxTokens {
			t.Fatalf("expected maxTokens to be clamped to %d, got %d", minMaxTokens, *p.maxTokens)
		}
	})
}
