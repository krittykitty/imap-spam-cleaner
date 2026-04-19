package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/dominicgisler/imap-spam-cleaner/logx"
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

	// Lenient fallback: try unmarshalling into a generic map and extract known fields.
	for _, body := range candidates {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			continue
		}
		var res AnalysisResponse
		if v, ok := getIntFromMap(m, "score", "spam_score"); ok {
			res.Score = v
		}
		if s, ok := getStringFromMap(m, "reason", "message"); ok {
			res.Reason = s
		}
		if b, ok := getBoolFromMap(m, "is_phishing", "isPhishing"); ok {
			res.IsPhishing = b
		}
		if b, ok := getBoolFromMap(m, "is_spam", "isSpam", "is_sp"); ok {
			res.IsSpam = b
		}

		// Accept lenient result if any known field was found
		if res.Score != 0 || res.Reason != "" || res.IsPhishing || res.IsSpam {
			return res, nil
		}
	}

	// Try partial recovery: extract Score and Reason from broken JSON (e.g., LLM output cut off)
	if partialRes, scoreFound := extractPartialAnalysisResponse(raw); scoreFound {
		snippet := trimmed
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		logx.Warnf("Recovered partial JSON analysis response for broken input: score=%d reason=%q (payload sample: %q)", partialRes.Score, partialRes.Reason, snippet)
		return partialRes, nil
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

// Helper extractors for lenient map-based parsing
func getIntFromMap(m map[string]interface{}, keys ...string) (int, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch t := v.(type) {
			case float64:
				return int(t), true
			case int:
				return t, true
			case int64:
				return int(t), true
			case string:
				var i int
				if _, err := fmt.Sscanf(t, "%d", &i); err == nil {
					return i, true
				}
			}
		}
	}
	return 0, false
}

func getStringFromMap(m map[string]interface{}, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch t := v.(type) {
			case string:
				return t, true
			}
		}
	}
	return "", false
}

func getBoolFromMap(m map[string]interface{}, keys ...string) (bool, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch t := v.(type) {
			case bool:
				return t, true
			case string:
				if strings.EqualFold(t, "true") {
					return true, true
				}
				if strings.EqualFold(t, "false") {
					return false, true
				}
			case float64:
				if t == 1 {
					return true, true
				}
				if t == 0 {
					return false, true
				}
			}
		}
	}
	return false, false
}

func extractJSONObject(value string) string {
	start := strings.IndexByte(value, '{')
	end := strings.LastIndexByte(value, '}')
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(value[start : end+1])
}

// extractPartialAnalysisResponse attempts to extract Score and Reason from broken/incomplete JSON.
// This handles cases where LLMs are cut off mid-output (e.g., Ollama with fixed context window).
// Returns (response, scoreFound) - scoreFound indicates if the critical Score field was extracted.
func extractPartialAnalysisResponse(raw string) (AnalysisResponse, bool) {
	var res AnalysisResponse

	// Extract score using regex: "score": followed by a number
	scoreRegex := regexp.MustCompile(`"score"\s*:\s*(\d+)`)
	scoreMatches := scoreRegex.FindStringSubmatch(raw)
	if len(scoreMatches) < 2 {
		// Score is required for recovery
		return res, false
	}

	// Parse the extracted score
	var score int
	fmt.Sscanf(scoreMatches[1], "%d", &score)
	res.Score = score

	// Extract reason using regex: "reason": followed by quoted string
	// Handles both complete ("reason": "text") and truncated ("reason": "text without closing quote)
	// Also handles escaped quotes within the text
	reasonRegex := regexp.MustCompile(`"reason"\s*:\s*"((?:[^"\\]|\\.)*)`)
	reasonMatches := reasonRegex.FindStringSubmatch(raw)
	if len(reasonMatches) >= 2 {
		reas := reasonMatches[1]
		// Unescape common JSON escape sequences
		reas = strings.ReplaceAll(reas, `\"`, `"`)
		reas = strings.ReplaceAll(reas, `\\`, `\`)
		reas = strings.ReplaceAll(reas, `\n`, "\n")
		reas = strings.ReplaceAll(reas, `\t`, "\t")
		res.Reason = reas
	}

	// Extract is_phishing if present; defaults to false if not found
	phishingRegex := regexp.MustCompile(`"is_phishing"\s*:\s*(true|false)`)
	phishingMatches := phishingRegex.FindStringSubmatch(raw)
	if len(phishingMatches) >= 2 {
		res.IsPhishing = phishingMatches[1] == "true"
	}

	// Extract is_spam if present; defaults to false if not found
	spamRegex := regexp.MustCompile(`"(?:is_spam|isSpam|is_sp)"\s*:\s*(true|false)`)
	spamMatches := spamRegex.FindStringSubmatch(raw)
	if len(spamMatches) >= 2 {
		res.IsSpam = spamMatches[1] == "true"
	}

	return res, true
}
