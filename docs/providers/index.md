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
You are a spam classification assistant. Analyze emails objectively and return only a JSON object with the fields score, reason, and is_phishing. Only return the JSON object, no other text.
```

### Default user prompt

```
Analyze the following email for its spam potential.
Return your analysis as a JSON object with the following fields:
{
  "score": <int 0-100>,
  "reason": "<short explanation of why this score was given>",
  "is_phishing": <bool>
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

Text body:
{{.TextBody}}

HTML body:
{{.HtmlBody}}
```

### Template variables

| Variable        | Description                                                          |
|-----------------|----------------------------------------------------------------------|
| `{{.Headers}}`  | Selected authentication/routing headers (Received, DKIM-Signature…) |
| `{{.From}}`     | Sender address                                                       |
| `{{.To}}`       | Primary recipient(s)                                                 |
| `{{.DeliveredTo}}` | Delivered-To header value                                         |
| `{{.Cc}}`       | CC recipients                                                        |
| `{{.Bcc}}`      | BCC recipients                                                       |
| `{{.Subject}}`  | Message subject                                                      |
| `{{.Body}}`     | Email body (HTML converted to Markdown when available)               |

