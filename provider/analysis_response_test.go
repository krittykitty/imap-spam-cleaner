package provider

import (
	"errors"
	"testing"
)

func TestParseAnalysisResponse(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		expectScore int
		wantErr     bool
	}{
		{
			name:        "plain json",
			raw:         `{"score": 12, "reason": "ok", "is_phishing": false}`,
			expectScore: 12,
		},
		{
			name:        "fenced json",
			raw:         "```json\n{\n  \"score\": 77,\n  \"reason\": \"suspicious links\",\n  \"is_phishing\": true\n}\n```",
			expectScore: 77,
		},
		{
			name:        "json wrapped in text",
			raw:         `Result: {"score": 45, "reason": "marketing", "is_phishing": false}`,
			expectScore: 45,
		},
		{
			name:    "invalid payload",
			raw:     "not-json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := parseAnalysisResponse(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !IsNonRetryable(err) {
					t.Fatalf("expected non-retryable error, got: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseAnalysisResponse failed: %v", err)
			}
			if res.Score != tt.expectScore {
				t.Fatalf("expected score %d, got %d", tt.expectScore, res.Score)
			}
		})
	}
}

func TestParseAnalysisResponse_EmptyPayloadIsRetryable(t *testing.T) {
	_, err := parseAnalysisResponse("   \n\t")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if IsNonRetryable(err) {
		t.Fatalf("expected retryable error for empty payload, got non-retryable: %v", err)
	}
}

func TestParseAnalysisResponse_InvalidPayloadIsNonRetryable(t *testing.T) {
	_, err := parseAnalysisResponse("not-json")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !IsNonRetryable(err) {
		t.Fatalf("expected non-retryable error for invalid non-empty payload, got: %v", err)
	}
}

func TestIsNonRetryable(t *testing.T) {
	err := markNonRetryable(errors.New("deterministic failure"))
	if !IsNonRetryable(err) {
		t.Fatalf("expected non-retryable marker")
	}
}
