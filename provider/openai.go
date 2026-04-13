package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/sashabaranov/go-openai"
)

type OpenAI struct {
	AIBase
	client *openai.Client
	apikey string
}

func (p *OpenAI) Name() string {
	return "openai"
}

func (p *OpenAI) ValidateConfig(config map[string]string) error {

	if err := p.AIBase.ValidateConfig(config); err != nil {
		return err
	}

	if config["apikey"] == "" {
		return errors.New("openai apikey is required")
	}
	p.apikey = config["apikey"]

	return nil
}

func (p *OpenAI) Init(config map[string]string) error {
	if err := p.ValidateConfig(config); err != nil {
		return err
	}
	p.client = openai.NewClient(p.apikey)
	return nil
}

func (p *OpenAI) HealthCheck(config map[string]string) error {
	if err := p.Init(config); err != nil {
		return err
	}
	return checkTCP("api.openai.com:443", 5*time.Second)
}

func (p *OpenAI) Analyze(msg imap.Message) (AnalysisResponse, error) {

	userContent, err := p.buildUserPrompt(msg)
	if err != nil {
		return AnalysisResponse{}, err
	}

	messages := []openai.ChatCompletionMessage{}
	if p.systemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: p.systemPrompt,
		})
	}
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: userContent,
	})

	req := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: messages,
	}
	if p.temperature != nil {
		req.Temperature = *p.temperature
	}
	if p.topP != nil {
		req.TopP = *p.topP
	}
	if p.maxTokens != nil {
		req.MaxCompletionTokens = int(*p.maxTokens)
	}

	resp, err := p.client.CreateChatCompletion(context.Background(), req)

	if err != nil {
		return AnalysisResponse{}, err
	}

	if len(resp.Choices) == 0 {
		return AnalysisResponse{}, errors.New("empty openai response")
	}

	var res AnalysisResponse
	body := strings.TrimSpace(resp.Choices[0].Message.Content)
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		return AnalysisResponse{}, err
	}

	logx.Infof("Reasoning for message #%d: %s", msg.UID, res.Reason)
	return res, nil
}

func (p *OpenAI) Consolidate(contextText string) (string, error) {
	// Backward-compatible wrapper: build vars with previous consolidation
	return p.ConsolidateVars(ConsolidationPromptVars{PreviousConsolidation: contextText})
}

func (p *OpenAI) ConsolidateVars(vars ConsolidationPromptVars) (string, error) {
	prompt, err := p.AIBase.buildConsolidationPrompt(vars)
	if err != nil {
		return "", err
	}

	messages := []openai.ChatCompletionMessage{}
	systemPrompt := p.systemPrompt
	if p.consolidationSystemPrompt != "" {
		systemPrompt = p.consolidationSystemPrompt
	}
	if systemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: prompt,
	})

	req := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: messages,
	}
	if p.temperature != nil {
		req.Temperature = *p.temperature
	}
	if p.topP != nil {
		req.TopP = *p.topP
	}
	if p.maxTokens != nil {
		req.MaxCompletionTokens = int(*p.maxTokens)
	}

	resp, err := p.client.CreateChatCompletion(context.Background(), req)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", errors.New("empty openai response")
	}

	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}
