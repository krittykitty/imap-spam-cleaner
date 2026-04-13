package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/ollama/ollama/api"
)

type Ollama struct {
	AIBase
	client *api.Client
	url    *url.URL
}

func (p *Ollama) Name() string {
	return "ollama"
}

func (p *Ollama) ValidateConfig(config map[string]string) error {

	if err := p.AIBase.ValidateConfig(config); err != nil {
		return err
	}

	if config["url"] == "" {
		return errors.New("ollama url is required")
	}

	u, err := url.Parse(config["url"])
	if err != nil {
		return err
	}
	p.url = u

	return nil
}

func (p *Ollama) Init(config map[string]string) error {
	if err := p.ValidateConfig(config); err != nil {
		return err
	}
	p.client = api.NewClient(p.url, http.DefaultClient)
	return nil
}

func (p *Ollama) HealthCheck(config map[string]string) error {
	if err := p.Init(config); err != nil {
		return err
	}

	host := p.url.Hostname()
	port := p.url.Port()
	if port == "" {
		if p.url.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return checkTCP(net.JoinHostPort(host, port), 5*time.Second)
}

func (p *Ollama) Analyze(msg imap.Message) (int, error) {

	prompt, err := p.AIBase.buildPrompt(msg)
	if err != nil {
		return 0, err
	}

	b := false
	req := api.GenerateRequest{
		Model:  p.model,
		Prompt: prompt,
		Stream: &b,
	}

	var resp string
	if err = p.client.Generate(context.Background(), &req, func(response api.GenerateResponse) error {
		resp = response.Response
		return nil
	}); err != nil {
		return 0, err
	}

	var res AnalysisResponse
	body := strings.TrimSpace(resp)
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		return 0, err
	}

	logx.Infof("Reasoning for message #%d: %s", msg.UID, res.Reason)
	return res.Score, nil
}
