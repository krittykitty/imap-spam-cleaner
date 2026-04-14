package provider

import (
	"testing"
)

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
