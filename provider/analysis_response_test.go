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
			name:        "json with extra fields and is_spam",
			raw:         `{"score": 30, "reason": "promo", "is_spam": true, "is_suspicious": true, "spam_score": 30}`,
			expectScore: 30,
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

// TestParseAnalysisResponse_BrokenJSON tests partial recovery from incomplete/broken JSON
func TestParseAnalysisResponse_BrokenJSON(t *testing.T) {
	tests := []struct {
		name           string
		raw            string
		expectScore    int
		expectReason   string
		expectPhishing bool
		shouldRecover  bool
	}{
		{
			name:           "broken json with score and reason",
			raw:            `Some text here {"score": 75, "reason": "suspicious links and attachments`,
			expectScore:    75,
			expectReason:   "suspicious links and attachments",
			expectPhishing: false,
			shouldRecover:  true,
		},
		{
			name:           "broken json truncated mid-reason",
			raw:            `{"score": 42, "reason": "This is a very long reason that got trun`,
			expectScore:    42,
			expectReason:   "This is a very long reason that got trun",
			expectPhishing: false,
			shouldRecover:  true,
		},
		{
			name:           "broken json with escaped quotes in reason",
			raw:            `{"score": 88, "reason": "Contains \"quoted\" text`,
			expectScore:    88,
			expectReason:   `Contains "quoted" text`,
			expectPhishing: false,
			shouldRecover:  true,
		},
		{
			name:           "broken json with only score",
			raw:            `{"score": 55, "reason":`,
			expectScore:    55,
			expectReason:   "",
			expectPhishing: false,
			shouldRecover:  true,
		},
		{
			name:           "broken json with is_phishing true",
			raw:            `{"score": 95, "reason": "phishing attempt", "is_phishing": true, "extra": "...`,
			expectScore:    95,
			expectReason:   "phishing attempt",
			expectPhishing: true,
			shouldRecover:  true,
		},
		{
			name:           "broken json with is_spam true",
			raw:            `{"score": 85, "reason": "clearly spammy", "is_spam": true, "extra": "...`,
			expectScore:    85,
			expectReason:   "clearly spammy",
			expectPhishing: false,
			shouldRecover:  true,
		},
		{
			name:           "text with partial json no score",
			raw:            `Result: {"reason": "no score field", "is_phishing": false`,
			expectScore:    0,
			expectReason:   "",
			expectPhishing: false,
			shouldRecover:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := parseAnalysisResponse(tt.raw)

			if tt.shouldRecover {
				// Should successfully recover
				if err != nil {
					t.Fatalf("expected recovery but got error: %v", err)
				}
				if res.Score != tt.expectScore {
					t.Errorf("expected score %d, got %d", tt.expectScore, res.Score)
				}
				if res.Reason != tt.expectReason {
					t.Errorf("expected reason %q, got %q", tt.expectReason, res.Reason)
				}
				if res.IsPhishing != tt.expectPhishing {
					t.Errorf("expected is_phishing %v, got %v", tt.expectPhishing, res.IsPhishing)
				}
			} else {
				// Should fail (no Score found)
				if err == nil {
					t.Fatalf("expected error for unrecoverable JSON, got nil")
				}
				if !IsNonRetryable(err) {
					t.Fatalf("expected non-retryable error, got: %v", err)
				}
			}
		})
	}
}

// TestExtractPartialAnalysisResponse tests the partial extraction function directly
func TestExtractPartialAnalysisResponse(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		expectScore   int
		expectReason  string
		expectSuccess bool
	}{
		{
			name:          "valid extraction with score and reason",
			raw:           `{"score": 65, "reason": "spam indicators"`,
			expectScore:   65,
			expectReason:  "spam indicators",
			expectSuccess: true,
		},
		{
			name:          "score with various spacing",
			raw:           `{ "score" : 44 , "reason": "test"`,
			expectScore:   44,
			expectReason:  "test",
			expectSuccess: true,
		},
		{
			name:          "no score field",
			raw:           `{"reason": "test", "is_phishing": true`,
			expectSuccess: false,
		},
		{
			name:          "score not a number",
			raw:           `{"score": "high", "reason": "bad format"`,
			expectSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, success := extractPartialAnalysisResponse(tt.raw)

			if success != tt.expectSuccess {
				t.Errorf("expected success %v, got %v", tt.expectSuccess, success)
			}

			if tt.expectSuccess {
				if res.Score != tt.expectScore {
					t.Errorf("expected score %d, got %d", tt.expectScore, res.Score)
				}
				if res.Reason != tt.expectReason {
					t.Errorf("expected reason %q, got %q", tt.expectReason, res.Reason)
				}
			}
		})
	}
}
