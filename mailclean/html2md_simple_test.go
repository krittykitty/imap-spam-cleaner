package mailclean

import (
	"os"
	"strings"
	"testing"
)

func TestHTMLToSimpleMarkdown_Simple(t *testing.T) {
	f, err := os.Open("testdata/simple.html")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	out, err := HTMLToSimpleMarkdown(f)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}

	// Heading should be converted
	if !strings.Contains(out, "# Special Offer Just For You!") {
		t.Errorf("expected h1 heading, got:\n%s", out)
	}

	// Link should be formatted as Markdown
	if !strings.Contains(out, "[claim your prize](https://example.com/buy)") {
		t.Errorf("expected markdown link, got:\n%s", out)
	}

	// List items should be present
	if !strings.Contains(out, "- 50% off all products") {
		t.Errorf("expected list item, got:\n%s", out)
	}

	// Quoted reply heuristic: "On Mon..." is >200 chars in, so it should be stripped
	if strings.Contains(out, "John Doe wrote:") {
		t.Errorf("quoted reply should have been stripped, got:\n%s", out)
	}

	// Script/style tags should not appear in output
	if strings.Contains(out, "font-family") {
		t.Errorf("style content should be stripped, got:\n%s", out)
	}
}

func TestHTMLToSimpleMarkdown_QuotedReply(t *testing.T) {
	f, err := os.Open("testdata/quoted_reply.html")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	out, err := HTMLToSimpleMarkdown(f)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}

	// Main content should be present
	if !strings.Contains(out, "Please find the invoice attached") {
		t.Errorf("expected main body content, got:\n%s", out)
	}

	// The "-----Original Message-----" marker should strip everything after it
	if strings.Contains(out, "can you send me the invoice") {
		t.Errorf("quoted reply content should have been stripped, got:\n%s", out)
	}
}

func TestHTMLToSimpleMarkdown_EmptyInput(t *testing.T) {
	out, err := HTMLToSimpleMarkdown(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error on empty input: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output for empty input, got: %q", out)
	}
}

func TestHTMLToSimpleMarkdown_PlainText(t *testing.T) {
	out, err := HTMLToSimpleMarkdown(strings.NewReader("Hello, world!"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Hello, world!") {
		t.Errorf("expected plain text to pass through, got: %q", out)
	}
}

func TestHTMLToSimpleMarkdown_LinkMismatchAndDomains(t *testing.T) {
	input := `<p>Click <a href="https://example.com">https://example.com</a> and <a href="http://phish-site.net/login">https://yourbank.com/login</a> now.</p>`
	out, err := HTMLToSimpleMarkdown(strings.NewReader(input))
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	if !strings.HasPrefix(out, "Link-Domains:") {
		t.Errorf("expected link domain summary at the top, got: %q", out)
	}
	if !strings.Contains(out, "example.com") || !strings.Contains(out, "phish-site.net") {
		t.Errorf("expected unique domains in summary, got: %q", out)
	}
	if !strings.Contains(out, "!! LINK MISMATCH !!") {
		t.Errorf("expected mismatch warning in output, got: %q", out)
	}
	if !strings.Contains(out, "[https://yourbank.com/login](http://phish-site.net/login)") {
		t.Errorf("expected markdown link with href preserved, got: %q", out)
	}
}

func TestHTMLToSimpleMarkdown_ScriptStripped(t *testing.T) {
	input := `<html><body><script>alert('xss')</script><p>Clean content</p></body></html>`
	out, err := HTMLToSimpleMarkdown(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "alert") {
		t.Errorf("script content should be stripped, got: %q", out)
	}
	if !strings.Contains(out, "Clean content") {
		t.Errorf("expected body content, got: %q", out)
	}
}
