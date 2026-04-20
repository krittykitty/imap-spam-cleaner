package inbox

import (
	"testing"

	"github.com/dominicgisler/imap-spam-cleaner/provider"
)

func TestFirstSenderEmail(t *testing.T) {
	tests := []struct {
		name string
		from string
		want string
	}{
		{name: "display name", from: "Alice <alice@example.com>", want: "alice@example.com"},
		{name: "multiple addresses", from: "alice@example.com, bob@example.com", want: "alice@example.com"},
		{name: "empty", from: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstSenderEmail(tt.from)
			if got != tt.want {
				t.Fatalf("firstSenderEmail() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShouldAutoWhitelistNonSpam(t *testing.T) {
	tests := []struct {
		name     string
		analysis provider.AnalysisResponse
		minScore int
		want     bool
	}{
		{
			name:     "clean classification below threshold",
			analysis: provider.AnalysisResponse{Score: 12, IsSpam: false, IsPhishing: false},
			minScore: 50,
			want:     true,
		},
		{
			name:     "score above threshold",
			analysis: provider.AnalysisResponse{Score: 70, IsSpam: false, IsPhishing: false},
			minScore: 50,
			want:     false,
		},
		{
			name:     "explicit spam flag",
			analysis: provider.AnalysisResponse{Score: 10, IsSpam: true, IsPhishing: false},
			minScore: 50,
			want:     false,
		},
		{
			name:     "explicit phishing flag",
			analysis: provider.AnalysisResponse{Score: 10, IsSpam: false, IsPhishing: true},
			minScore: 50,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldAutoWhitelistNonSpam(tt.analysis, tt.minScore)
			if got != tt.want {
				t.Fatalf("shouldAutoWhitelistNonSpam() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMessageIDForTracking(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name:    "canonical message-id header",
			headers: map[string]string{"Message-ID": "<AbC-123@example.com>"},
			want:    "abc-123@example.com",
		},
		{
			name:    "alternate message-id header",
			headers: map[string]string{"Message-Id": " xyz@example.com "},
			want:    "xyz@example.com",
		},
		{
			name:    "missing",
			headers: map[string]string{"Subject": "Hello"},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messageIDForTracking(tt.headers)
			if got != tt.want {
				t.Fatalf("messageIDForTracking() = %q, want %q", got, tt.want)
			}
		})
	}
}
