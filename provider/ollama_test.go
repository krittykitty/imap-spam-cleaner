package provider

import "testing"

func TestOllamaValidateConfigNumCtxFromConfig(t *testing.T) {
	p := &Ollama{}
	cfg := map[string]string{
		"model":   "test-model",
		"maxsize": "1000",
		"url":     "http://127.0.0.1:11434",
		"num_ctx": "3072",
	}

	if err := p.ValidateConfig(cfg); err != nil {
		t.Fatalf("ValidateConfig failed: %v", err)
	}
	if p.contextWindow == nil {
		t.Fatal("expected contextWindow to be set")
	}
	if got := *p.contextWindow; got != 3072 {
		t.Fatalf("expected contextWindow=3072, got %d", got)
	}
}

func TestOllamaValidateConfigNumCtxFromEnv(t *testing.T) {
	t.Setenv("OLLAMA_NUM_CTX", "4096")

	p := &Ollama{}
	cfg := map[string]string{
		"model":   "test-model",
		"maxsize": "1000",
		"url":     "http://127.0.0.1:11434",
	}

	if err := p.ValidateConfig(cfg); err != nil {
		t.Fatalf("ValidateConfig failed: %v", err)
	}
	if p.contextWindow == nil {
		t.Fatal("expected contextWindow to be set from env")
	}
	if got := *p.contextWindow; got != 4096 {
		t.Fatalf("expected contextWindow=4096, got %d", got)
	}
}

func TestOllamaValidateConfigNumCtxConfigOverridesEnv(t *testing.T) {
	t.Setenv("OLLAMA_NUM_CTX", "4096")

	p := &Ollama{}
	cfg := map[string]string{
		"model":   "test-model",
		"maxsize": "1000",
		"url":     "http://127.0.0.1:11434",
		"num_ctx": "3072",
	}

	if err := p.ValidateConfig(cfg); err != nil {
		t.Fatalf("ValidateConfig failed: %v", err)
	}
	if p.contextWindow == nil {
		t.Fatal("expected contextWindow to be set")
	}
	if got := *p.contextWindow; got != 3072 {
		t.Fatalf("expected contextWindow from config to win (3072), got %d", got)
	}
}

func TestOllamaValidateConfigNumCtxInvalid(t *testing.T) {
	p := &Ollama{}
	cfg := map[string]string{
		"model":   "test-model",
		"maxsize": "1000",
		"url":     "http://127.0.0.1:11434",
		"num_ctx": "0",
	}

	if err := p.ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for num_ctx=0")
	}
}
