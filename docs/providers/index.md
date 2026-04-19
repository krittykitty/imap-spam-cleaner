# Providers

- [OpenAI](openai.md)
- [Ollama](ollama.md)
- [Gemini](gemini.md)
- [SpamAssassin](spamassassin.md)

## AI prompt defaults

All AI providers (OpenAI, Ollama, Gemini) use a two-part prompt model:

| Key             | Role    | Purpose                                              |
|-----------------|---------|------------------------------------------------------|
| `system_prompt` | system  | Sets the AI persona / standing instructions          |
| `user_prompt`   | user    | Carries the email data; uses Go template variables   |

The legacy `prompt` key is still accepted for backward compatibility and maps to `user_prompt`.

### Default system prompt

```
You are a spam classification assistant. Analyze emails objectively and return only a JSON object with the fields score, reason, is_phishing, and is_spam. Only return the JSON object, no other text.
```

### Default user prompt

```
Analyze the following email for its spam potential.
Return your analysis as a JSON object with the following fields:
{
  "score": <int 0-100>,
  "is_phishing": <bool>,
  "is_spam": <bool>,
  "reason": "<short explanation of why this score was given>"
}
Only return the JSON. No other text.

Headers:
{{.Headers}}

From: {{.From}}
To: {{.To}}
Delivered-To: {{.DeliveredTo}}
Cc: {{.Cc}}
Bcc: {{.Bcc}}
Subject: {{.Subject}}

Body (HTML converted to Markdown when available):
{{.Body}}
```

// Default consolidation prompts and template variables are archived. See `archive/` for legacy usage.

