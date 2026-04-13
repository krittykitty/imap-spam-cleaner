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
