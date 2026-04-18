package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ollama/ollama/api"
)

func main() {
	model := "granite3.1-moe:3b"
	ollamaURL := "http://127.0.0.1:11434"
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		ollamaURL = v
	}
	client := api.NewClientFromString(ollamaURL, nil)
	prompt := "test"
	b := false
	ctx := context.Background()
	
	req := api.GenerateRequest{
		Model:  model,
		Prompt: prompt,
		Stream: &b,
		Format: json.RawMessage(`"json"`),
		Options: map[string]interface{}{
			"num_ctx": 3072,
		},
	}
	var resp string
	var promptEvalCount, evalCount int
	var thinkingFrames, thinkingChars int
	var lastDoneReason string
	err := client.Generate(ctx, &req, func(r api.GenerateResponse) error {
		resp += r.Response
		if r.PromptEvalCount > 0 {
			promptEvalCount = r.PromptEvalCount
		}
		if r.EvalCount > 0 {
			evalCount = r.EvalCount
		}
		if strings.TrimSpace(r.Thinking) != "" {
			thinkingFrames++
			thinkingChars += len(r.Thinking)
		}
		if r.DoneReason != "" {
			lastDoneReason = r.DoneReason
		}
		return nil
	})
	if err != nil {
		fmt.Println("Ollama error:", err)
		os.Exit(1)
	}
	fmt.Printf("Response: %q\n", strings.TrimSpace(resp))
	fmt.Printf("prompt_eval_count: %d\n", promptEvalCount)
	fmt.Printf("eval_count: %d\n", evalCount)
	fmt.Printf("thinking_frames: %d\n", thinkingFrames)
	fmt.Printf("thinking_chars: %d\n", thinkingChars)
	fmt.Printf("done_reason: %q\n", lastDoneReason)
}
