# Providers

| Field         | Type    | Required | Default | Description                                              | Options                                          |
|---------------|---------|----------|---------|----------------------------------------------------------|--------------------------------------------------|
| `type`        | string  | yes      |         | Provider implementation                                  | `openai`, `ollama`, `spamassassin`, `gemini`     |
| `concurrency` | integer | no       | `1`     | Maximum number of parallel `Analyze` calls for this provider | `2`                                          |
| `rate_limit`  | float   | no       | `0`     | Maximum `Analyze` calls per second; `0` = unlimited      | `5.0`                                            |
| `config`      | object  | yes      |         | Provider-specific configuration                          |                                                  |

A list of providers, which can be reused by inboxes.
Each provider is identified by a name (`prov1` in the following example) and will be referenced by that name.
You can find a detailed description for the options on the individual provider pages.

`concurrency` and `rate_limit` control the per-provider worker pool used by the [IMAP IDLE](inboxes.md) processing path.
For cron-scheduled inboxes these settings are informational; for IDLE inboxes they directly bound how many messages are
analysed in parallel and how fast API calls are made.

Example:

```yaml
providers:
  prov1:
    type: openai
    concurrency: 2      # up to 2 parallel Analyze calls
    rate_limit: 5.0     # at most 5 API calls per second
    config:
      apikey: some-api-key
      model: gpt-4o-mini
      maxsize: 100000
```

