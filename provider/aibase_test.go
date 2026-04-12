package provider

import (
	"strings"
	"testing"
	"text/template"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
)

func TestParseSpamScore(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{name: "plain number", in: "92", want: 92},
		{name: "markdown bold", in: "**Spam Score: 92**", want: 92},
		{name: "with suffix", in: "92/100", want: 92},
		{name: "with prefix", in: "Score is 78. Thanks", want: 78},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseSpamScore(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestBuildUserPromptUsesOnlyOneBody(t *testing.T) {
	p := AIBase{maxsize: 1024}
	p.userPrompt = template.Must(template.New("user_prompt").Parse(defaultUserPrompt))

	msg := imap.Message{
		UID:      1,
		From:     "alice@example.com",
		To:       "bob@example.com",
		Subject:  "Test",
		Headers:  "Message-ID: <1@example.com>",
		TextBody: "This is plain text.",
		HtmlBody: "**This is cleaned HTML.**",
	}

	prompt, err := p.buildUserPrompt(msg)
	if err != nil {
		t.Fatalf("buildUserPrompt failed: %v", err)
	}

	if strings.Contains(prompt, "Text body:") {
		t.Fatalf("prompt should not include text body labels after body selection")
	}
	if !strings.Contains(prompt, "This is cleaned HTML.") {
		t.Fatalf("expected html body in prompt, got: %s", prompt)
	}
}
