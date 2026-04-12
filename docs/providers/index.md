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
You are a spam classification assistant. Analyze emails objectively and return only a single integer score.
```

### Default user prompt

```
Analyze the following email for its spam potential.
Return a spam score between 0 and 100. Only answer with the number itself, no other text.

Headers:
{{.Headers}}

From: {{.From}}
To: {{.To}}
Delivered-To: {{.DeliveredTo}}
Cc: {{.Cc}}
Bcc: {{.Bcc}}
Subject: {{.Subject}}

Email body:
{{.Body}}
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

