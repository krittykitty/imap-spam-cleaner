package provider

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"

	"google.golang.org/genai"
)

type Gemini struct {
	AIBase
	client *genai.Client
	apikey string
}

func (p *Gemini) Name() string {
	return "gemini"
}

func (p *Gemini) ValidateConfig(config map[string]string) error {

	if err := p.AIBase.ValidateConfig(config); err != nil {
		return err
	}

	if config["apikey"] == "" {
		return errors.New("gemini apikey is required")
	}
	p.apikey = config["apikey"]

	return nil
}

func (p *Gemini) Init(config map[string]string) error {

	if err := p.ValidateConfig(config); err != nil {
		return err
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey: p.apikey,
	})
	if err != nil {
		return err
	}

	p.client = client

	return nil
}

func (p *Gemini) HealthCheck(config map[string]string) error {
	if err := p.Init(config); err != nil {
		return err
	}
	return checkTCP("generativeai.googleapis.com:443", 5*time.Second)
}

func (p *Gemini) Analyze(msg imap.Message) (AnalysisResponse, error) {

	userContent, err := p.buildUserPrompt(msg)
	if err != nil {
		return AnalysisResponse{}, err
	}

	cfg := &genai.GenerateContentConfig{}
	if p.systemPrompt != "" {
		cfg.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: p.systemPrompt}},
		}
	}
	if p.temperature != nil {
		cfg.Temperature = p.temperature
	}
	if p.topP != nil {
		cfg.TopP = p.topP
	}
	cfg.MaxOutputTokens = p.effectiveMaxTokens()

	resp, err := p.client.Models.GenerateContent(
		context.Background(),
		p.model,
		genai.Text(userContent),
		cfg,
	)

	if err != nil {
		return AnalysisResponse{}, err
	}

	if len(resp.Candidates) == 0 ||
		len(resp.Candidates[0].Content.Parts) == 0 {
		return AnalysisResponse{}, errors.New("empty gemini response")
	}

	var res AnalysisResponse
	body := strings.TrimSpace(resp.Candidates[0].Content.Parts[0].Text)
	res, err = parseAnalysisResponse(body)
	if err != nil {
		return AnalysisResponse{}, err
	}

	logx.Infof("Reasoning for message #%d: score=%d phishing=%t reason=%s", msg.UID, res.Score, res.IsPhishing, res.Reason)
	return res, nil
}
