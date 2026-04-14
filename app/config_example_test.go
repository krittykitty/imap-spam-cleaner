package app_test

import (
	"os"
	"testing"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
)

func TestConfigExampleValid(t *testing.T) {
	data, err := os.ReadFile("../config.example.yml")
	if err != nil {
		t.Fatalf("could not read config.example.yml: %v", err)
	}

	// write a temporary config.yml for LoadConfig to read
	if err := os.WriteFile("config.yml", data, 0644); err != nil {
		t.Fatalf("could not write config.yml: %v", err)
	}
	defer os.Remove("config.yml")

	cfg, err := app.LoadConfig()
	if err != nil {
		t.Fatalf("app.LoadConfig failed: %v", err)
	}

	if len(cfg.Providers) == 0 {
		t.Fatalf("no providers found in config.example.yml")
	}

	// Validate provider-specific configuration keys
	for name, p := range cfg.Providers {
		pr, err := provider.New(p.Type)
		if err != nil {
			t.Fatalf("provider.New failed for provider %s: %v", name, err)
		}
		if err := pr.ValidateConfig(p.Config); err != nil {
			t.Fatalf("provider %s config validation failed: %v", name, err)
		}
	}
}
