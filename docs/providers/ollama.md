# Ollama

Uses a locally running Ollama LLM server to analyze the message.

Configuration options:

| Field           | Type    | Required | Description                                                                           | Example                  |
|-----------------|---------|----------|---------------------------------------------------------------------------------------|--------------------------|
| `url`           | string  | yes      | Ollama server URL                                                                     | `http://127.0.0.1:11434` |
| `model`         | string  | yes      | Ollama model name used for classification                                             | `gpt-oss:20b`            |
| `maxsize`       | integer | yes      | Maximum email size sent to the model (bytes); content is truncated if exceeded        | `100000`                 |
| `system_prompt` | string  | no       | System role instructions — sets the AI persona (replaces default system prompt)       | _see below_              |
| `user_prompt`   | string  | no       | User role template with email data; supports Go template variables (replaces `prompt`)| _see below_              |
| `prompt`        | string  | no       | **Deprecated.** Legacy combined prompt; mapped to `user_prompt` for compatibility     |                          |
| `temperature`   | float   | no       | Sampling temperature (0.0–2.0); lower = more deterministic                            | `0.2`                    |
| `top_p`         | float   | no       | Nucleus sampling probability (0.0–1.0)                                                | `0.95`                   |
| `max_tokens`    | integer | no       | Maximum tokens in the response (maps to `num_predict` in Ollama)                     | `512`                    |

See [Providers](index.md) for available template variables and default prompt values.

Example:

```yaml
providers:
  prov1:
    type: ollama
    concurrency: 1
    rate_limit: 0
    config:
      url: http://127.0.0.1:11434
      model: gpt-oss:20b
      maxsize: 100000
      # temperature: 0.2
      # top_p: 0.95
      # max_tokens: 512
      prompt: |
        Analyze the following email for its spam potential.
        Return your analysis as a JSON object with the following fields:
        {
          "score": <int 0-100>,
          "reason": "<short explanation of why this score was given>",
          "is_phishing": <bool>
        }
        Only return the JSON. No other text.

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

