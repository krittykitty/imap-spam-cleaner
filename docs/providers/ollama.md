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
    config:
      url: http://127.0.0.1:11434
      model: gpt-oss:20b
      maxsize: 100000
      # temperature: 0.2
      # top_p: 0.95
      # max_tokens: 512
      user_prompt: |
        Analyze the following email for its spam score.
        Don't sort out legit invoices, transactional or personal email, but sort out SPAM and pure advertising emails.
        Return a spam score between 0 and 100. Only output the integer.

        Recent context:
        {{.Context}}

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

Use `consolidation_prompt` inside the provider config and `consolidation_provider` in the inbox definition to run a different LLM provider for consolidation.

Use these keys to override consolidation-only behavior:
- `consolidation_model`
- `consolidation_system_prompt`
- `consolidation_user_prompt`
- `consolidation_prompt`

Top-level `defaults` entries (`system_prompt`, `user_prompt`, `consolidation_prompt`) are applied to all providers unless overridden. Use keys prefixed with `consolidation_` inside a provider config, such as `consolidation_model` or `consolidation_prompt`, to change only the consolidation run.

