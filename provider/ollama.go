package provider

import (
	"context"
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

func (p *Ollama) Analyze(msg imap.Message) (AnalysisResponse, error) {

	prompt, err := p.AIBase.buildPrompt(msg)
	if err != nil {
		return AnalysisResponse{}, err
	}

	b := false
	req := api.GenerateRequest{
		Model:  p.model,
		Prompt: prompt,
		Stream: &b,
	}
	if p.maxTokens != nil {
		req.Options = map[string]interface{}{
			"num_predict": int(*p.maxTokens),
		}
	}

	var resp string
	if err = p.client.Generate(context.Background(), &req, func(response api.GenerateResponse) error {
		resp = response.Response
		return nil
	}); err != nil {
		return AnalysisResponse{}, err
	}

	var res AnalysisResponse
	body := strings.TrimSpace(resp)
	res, err = parseAnalysisResponse(body)
	if err != nil {
		return AnalysisResponse{}, err
	}

	logx.Infof("Reasoning for message #%d: %s", msg.UID, res.Reason)
	return res, nil
}

func (p *Ollama) Consolidate(contextText string) (string, error) {
	return p.ConsolidateVars(ConsolidationPromptVars{PreviousConsolidation: contextText})
}

func (p *Ollama) ConsolidateVars(vars ConsolidationPromptVars) (string, error) {
	prompt, err := p.AIBase.buildConsolidationPrompt(vars)
	if err != nil {
		return "", err
	}

	if p.consolidationSystemPrompt != "" {
		prompt = p.consolidationSystemPrompt + "\n\n" + prompt
	}

	b := false
	req := api.GenerateRequest{
		Model:  p.model,
		Prompt: prompt,
		Stream: &b,
	}
	if p.maxTokens != nil {
		req.Options = map[string]interface{}{
			"num_predict": int(*p.maxTokens),
		}
	}

	var resp string
	if err = p.client.Generate(context.Background(), &req, func(response api.GenerateResponse) error {
		resp = response.Response
		return nil
	}); err != nil {
		return "", err
	}

	return strings.TrimSpace(resp), nil
}
