package provider

import (
	"strings"
	"testing"
	"text/template"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
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

func newPromptTestProvider(t *testing.T, maxsize int) *AIBase {
	t.Helper()

	return &AIBase{
		maxsize: maxsize,
		userPrompt: template.Must(template.New("user_prompt").Parse(
			"Text={{.TextBody}}\nHTML={{.HtmlBody}}\nBody={{.Body}}",
		)),
	}
}

func TestBuildUserPrompt_PrefersHTMLAndIgnoresTextWhenHTMLExists(t *testing.T) {
	p := newPromptTestProvider(t, 1000)

	prompt, err := p.buildUserPrompt(imap.Message{
		UID:      1,
		Subject:  "subject",
		TextBody: "this text should be ignored",
		HtmlBody: "<p>html-body</p>",
	})
	if err != nil {
		t.Fatalf("buildUserPrompt failed: %v", err)
	}

	if strings.Contains(prompt, "this text should be ignored") {
		t.Fatalf("expected text body to be ignored when HTML exists, got: %q", prompt)
	}
	if !strings.Contains(prompt, "HTML=html-body") {
		t.Fatalf("expected HTML body to be used, got: %q", prompt)
	}
	if !strings.Contains(prompt, "Body=html-body") {
		t.Fatalf("expected Body to be the selected HTML content, got: %q", prompt)
	}
}

func TestBuildUserPrompt_FallsBackToTextWhenHTMLMissing(t *testing.T) {
	p := newPromptTestProvider(t, 1000)

	prompt, err := p.buildUserPrompt(imap.Message{
		UID:      2,
		Subject:  "subject",
		TextBody: "plain text fallback",
	})
	if err != nil {
		t.Fatalf("buildUserPrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "Text=plain text fallback") {
		t.Fatalf("expected text fallback body to be used, got: %q", prompt)
	}
	if !strings.Contains(prompt, "Body=plain text fallback") {
		t.Fatalf("expected Body to use plain text fallback, got: %q", prompt)
	}
}

func TestBuildUserPrompt_SoftCap_AllowsSlightlyOversizedBody(t *testing.T) {
	p := newPromptTestProvider(t, 10)

	body := "abcdefghijkl" // 12 chars == 120% of maxsize
	prompt, err := p.buildUserPrompt(imap.Message{
		UID:      3,
		Subject:  "subject",
		TextBody: body,
	})
	if err != nil {
		t.Fatalf("buildUserPrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "Body="+body) {
		t.Fatalf("expected full body within soft cap, got: %q", prompt)
	}
}

func TestBuildUserPrompt_SoftCap_TruncatesWhenOver120Percent(t *testing.T) {
	p := newPromptTestProvider(t, 10)

	body := "abcdefghijklm" // 13 chars > 120% of maxsize
	prompt, err := p.buildUserPrompt(imap.Message{
		UID:      4,
		Subject:  "subject",
		TextBody: body,
	})
	if err != nil {
		t.Fatalf("buildUserPrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "Body=abcdefghij") {
		t.Fatalf("expected body truncated to hard cap, got: %q", prompt)
	}
	if strings.Contains(prompt, "Body="+body) {
		t.Fatalf("did not expect full body when above soft cap, got: %q", prompt)
	}
}
