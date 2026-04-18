package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/ollama/ollama/api"
)

type Ollama struct {
	AIBase
	client        *api.Client
	url           *url.URL
	contextWindow *int32
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

	numCtxValue := strings.TrimSpace(config["num_ctx"])
	if numCtxValue == "" {
		numCtxValue = strings.TrimSpace(os.Getenv("OLLAMA_NUM_CTX"))
	}
	p.contextWindow = nil
	if numCtxValue != "" {
		n, err := strconv.ParseInt(numCtxValue, 10, 32)
		if err != nil || n < 1 {
			return errors.New("num_ctx must be a positive integer")
		}
		vv := int32(n)
		p.contextWindow = &vv
	}

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
		Format: json.RawMessage(`"json"`),
	}
	options := map[string]interface{}{
		"num_predict": int(p.effectiveMaxTokens()),
	}
	if p.contextWindow != nil {
		options["num_ctx"] = int(*p.contextWindow)
	}
	req.Options = options

	var resp string
	frames := 0
	nonEmptyFrames := 0
	lastDoneReason := ""
	promptEvalCount := 0
	evalCount := 0
	thinkingFrames := 0
	thinkingChars := 0
	if err = p.client.Generate(context.Background(), &req, func(response api.GenerateResponse) error {
		frames++
		if response.Response != "" {
			nonEmptyFrames++
		}
		if response.PromptEvalCount > 0 {
			promptEvalCount = response.PromptEvalCount
		}
		if response.EvalCount > 0 {
			evalCount = response.EvalCount
		}
		if strings.TrimSpace(response.Thinking) != "" {
			thinkingFrames++
			thinkingChars += len(response.Thinking)
		}
		resp += response.Response
		if response.DoneReason != "" {
			lastDoneReason = response.DoneReason
		}
		return nil
	}); err != nil {
		return AnalysisResponse{}, err
	}

	totalTokens := promptEvalCount + evalCount
	thinkingUsed := thinkingFrames > 0
	if p.contextWindow != nil {
		usagePercent := float64(totalTokens) * 100 / float64(*p.contextWindow)
		logx.Debugf("Ollama token usage for message #%d (model=%s prompt_tokens=%d completion_tokens=%d total_tokens=%d num_ctx=%d usage=%.1f%% thinking_used=%t thinking_frames=%d thinking_chars=%d)", msg.UID, p.model, promptEvalCount, evalCount, totalTokens, *p.contextWindow, usagePercent, thinkingUsed, thinkingFrames, thinkingChars)
	} else {
		logx.Debugf("Ollama token usage for message #%d (model=%s prompt_tokens=%d completion_tokens=%d total_tokens=%d num_ctx=default thinking_used=%t thinking_frames=%d thinking_chars=%d)", msg.UID, p.model, promptEvalCount, evalCount, totalTokens, thinkingUsed, thinkingFrames, thinkingChars)
	}

	var res AnalysisResponse
	body := strings.TrimSpace(resp)
	if body == "" {
		logx.Warnf("Ollama returned empty analysis body for message #%d (model=%s frames=%d nonEmptyFrames=%d doneReason=%q)", msg.UID, p.model, frames, nonEmptyFrames, lastDoneReason)
	} else {
		logx.Debugf("Ollama analysis response stats for message #%d (model=%s bytes=%d frames=%d nonEmptyFrames=%d doneReason=%q)", msg.UID, p.model, len(body), frames, nonEmptyFrames, lastDoneReason)
	}
	res, err = parseAnalysisResponse(body)
	if err != nil {
		return AnalysisResponse{}, err
	}

	logx.Infof("Reasoning for message #%d: score=%d phishing=%t reason=%s", msg.UID, res.Score, res.IsPhishing, res.Reason)
	return res, nil
}
