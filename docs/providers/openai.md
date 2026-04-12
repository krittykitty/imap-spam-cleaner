# OpenAI

Uses OpenAI (or any OpenAI-compatible API) to analyze the message.

Configuration options:

| Field           | Type    | Required | Description                                                                           | Example        |
|-----------------|---------|----------|---------------------------------------------------------------------------------------|----------------|
| `apikey`        | string  | yes      | OpenAI API key                                                                        | `some-api-key` |
| `model`         | string  | yes      | OpenAI model used for classification                                                  | `gpt-4o-mini`  |
| `maxsize`       | integer | yes      | Maximum email size sent to the model (bytes); content is truncated if exceeded        | `100000`       |
| `system_prompt` | string  | no       | System role instructions — sets the AI persona (replaces default system prompt)       | _see below_    |
| `user_prompt`   | string  | no       | User role template with email data; supports Go template variables (replaces `prompt`)| _see below_    |
| `prompt`        | string  | no       | **Deprecated.** Legacy combined prompt; mapped to `user_prompt` for compatibility     |                |
| `temperature`   | float   | no       | Sampling temperature (0.0–2.0); lower = more deterministic                            | `0.2`          |
| `top_p`         | float   | no       | Nucleus sampling probability (0.0–1.0)                                                | `0.95`         |
| `max_tokens`    | integer | no       | Maximum tokens in the response                                                        | `512`          |

See [Providers](index.md) for available template variables and default prompt values.

Example:

```yaml
providers:
  prov1:
    type: openai
    concurrency: 2
    rate_limit: 5.0
    config:
      apikey: some-api-key
      model: gpt-4o-mini
      maxsize: 100000
      # temperature: 0.2
      # top_p: 0.95
      # max_tokens: 512
      system_prompt: |
        You are a cybersecurity analyst specializing in email fraud.
        Your goal is to detect spam, phishing, and spoofed senders.
      user_prompt: |
        Analyze the following email for its spam potential.
        Return a spam score between 0 and 100. Only answer with the number itself.

        Headers:
        {{.Headers}}

        From: {{.From}}
        To: {{.To}}
        Delivered-To: {{.DeliveredTo}}
        Cc: {{.Cc}}
        Bcc: {{.Bcc}}
        Subject: {{.Subject}}

        Headers:
        {{.Headers}}

        Text body:
        {{.TextBody}}

        HTML body:
        {{.HtmlBody}}
```

