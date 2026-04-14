package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type nonRetryableError struct {
	err error
}

func (e nonRetryableError) Error() string {
	return e.err.Error()
}

func (e nonRetryableError) Unwrap() error {
	return e.err
}

func markNonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryableError{err: err}
}

func IsNonRetryable(err error) bool {
	var target nonRetryableError
	return errors.As(err, &target)
}

func parseAnalysisResponse(raw string) (AnalysisResponse, error) {
	candidates := make([]string, 0, 4)
	appendCandidate := func(value string) {
		value = strings.TrimSpace(strings.TrimPrefix(value, "\ufeff"))
		if value == "" {
			return
		}
		for _, existing := range candidates {
			if existing == value {
				return
			}
		}
		candidates = append(candidates, value)
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		// Empty model responses can be transient transport/runtime issues and
		// should remain retryable by the dispatcher.
		return AnalysisResponse{}, errors.New("empty analysis response")
	}

	appendCandidate(trimmed)

	unfenced := stripMarkdownCodeFence(trimmed)
	appendCandidate(unfenced)

	appendCandidate(extractJSONObject(trimmed))
	appendCandidate(extractJSONObject(unfenced))

	var lastErr error
	for _, body := range candidates {
		var res AnalysisResponse
		if err := json.Unmarshal([]byte(body), &res); err == nil {
			return res, nil
		} else {
			lastErr = err
		}
	}

	snippet := trimmed
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}

	if lastErr == nil {
		lastErr = errors.New("no JSON object found")
	}

	return AnalysisResponse{}, markNonRetryable(fmt.Errorf("invalid analysis JSON response: %w (payload starts with %q)", lastErr, snippet))
}

func stripMarkdownCodeFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}

	firstNewline := strings.IndexByte(value, '\n')
	if firstNewline == -1 {
		return strings.Trim(value, "`")
	}

	value = value[firstNewline+1:]
	if idx := strings.LastIndex(value, "```"); idx >= 0 {
		value = value[:idx]
	}

	return strings.TrimSpace(value)
}

func extractJSONObject(value string) string {
	start := strings.IndexByte(value, '{')
	end := strings.LastIndexByte(value, '}')
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(value[start : end+1])
}
