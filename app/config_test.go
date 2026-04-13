package app_test

import (
	"os"
	"testing"

	"github.com/dominicgisler/imap-spam-cleaner/app"
)

func TestLoadConfigAppliesConsolidationDefaults(t *testing.T) {
	data := []byte(`logging:
  level: debug
defaults:
  system_prompt: "SYS A"
  user_prompt: "USER A"
  consolidation_system_prompt: "SYS B"
  consolidation_user_prompt: "USER B"
  consolidation_prompt: "PROMPT B"
providers:
  prov1:
    type: openai
    config:
      apikey: some-api-key
      model: gpt-4o-mini
      maxsize: "100000"
inboxes:
  - schedule: "* * * * *"
    host: mail.domain.tld
    port: 143
    username: user@domain.tld
    password: mypass
    provider: prov1
    inbox: INBOX
    spam: INBOX.Spam
    minscore: 75
`)

	if err := os.WriteFile("config.yml", data, 0644); err != nil {
		t.Fatalf("could not write config.yml: %v", err)
	}
	defer os.Remove("config.yml")

	cfg, err := app.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	prov, ok := cfg.Providers["prov1"]
	if !ok {
		t.Fatalf("expect provider prov1")
	}

	if prov.Config["consolidation_system_prompt"] != "SYS B" {
		t.Fatalf("expected consolidation_system_prompt to be propagated, got %q", prov.Config["consolidation_system_prompt"])
	}
	if prov.Config["consolidation_user_prompt"] != "USER B" {
		t.Fatalf("expected consolidation_user_prompt to be propagated, got %q", prov.Config["consolidation_user_prompt"])
	}
	if prov.Config["consolidation_prompt"] != "PROMPT B" {
		t.Fatalf("expected consolidation_prompt to be propagated, got %q", prov.Config["consolidation_prompt"])
	}
}
