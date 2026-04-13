# Fork Changes — krittykitty/imap-spam-cleaner

This document describes every significant difference between this fork and the upstream project
[dominicgisler/imap-spam-cleaner](https://github.com/dominicgisler/imap-spam-cleaner).

---

## New features

### 1. Incremental mail processing with UID checkpointing (`checkpoint/`)

**Upstream behaviour:** Every cron run loaded all messages in the configured age window, re-fetching and potentially
re-analysing messages that had already been processed.

**Fork behaviour:** Each inbox now maintains a small JSON checkpoint file (in `checkpoints/`) that records the last
successfully processed IMAP UID and the mailbox's UIDVALIDITY value.  On subsequent runs only UIDs *above* the last
checkpoint are fetched and analysed.  The checkpoint advances atomically, message by message, so a crash or
provider error only causes that single message to be retried — not the entire window.

If UIDVALIDITY changes (mailbox was rebuilt), the checkpoint is automatically reset to the current maximum UID so
the next run starts fresh without reprocessing old mail.

**Why:** Eliminates redundant API calls, reduces cost and latency, and prevents double-moving messages.

---

### 2. IMAP IDLE support (`enable_idle` / `idle_timeout` inbox options)

**Upstream behaviour:** Only cron-scheduled polling was supported.

**Fork behaviour:** Setting `enable_idle: true` on an inbox switches it from cron polling to IMAP IDLE.  The
application keeps a persistent connection to the server and is notified by the server when new mail arrives,
triggering analysis within seconds instead of waiting for the next cron tick.

An initial catch-up run is always performed on startup to process messages that arrived while the daemon was down.
The IDLE connection is automatically re-issued every `idle_timeout` (default `25m`) to comply with RFC 2177, and
reconnects with exponential back-off on network errors.

**Why:** Near-instant spam detection without hammering the IMAP server with frequent polls.

---

### 3. Per-provider worker queues and dispatcher (`dispatcher/`)

**Upstream behaviour:** Analysis was always sequential — one message at a time per inbox run.

**Fork behaviour:** A new `Dispatcher` component creates a bounded channel and a pool of worker goroutines for each
configured provider.  `concurrency` controls pool size; `rate_limit` attaches a token-bucket limiter.  Workers
apply exponential back-off retry logic (configurable via `max_retries` on the inbox) before reporting a terminal
failure.

This component is used by the IDLE path.  Cron-scheduled inboxes are unaffected (they use a single sequential
local provider instance per run, as before).

**Why:** Allows multiple IDLE inboxes using the same provider to be analysed in parallel while still respecting
provider API rate limits.  The retry logic improves resilience against transient provider errors without
requeueing messages for the next inbox scan.

---

### 4. HTML→Markdown conversion (`mailclean/`)

**Upstream behaviour:** The raw HTML body was forwarded to the AI provider as-is.

**Fork behaviour:** HTML bodies are converted to simplified Markdown (stripped of `<style>`, `<script>`, and
non-content tags) before being included in the prompt.  If conversion fails, the raw HTML is used as a fallback.
When both a plain-text and an HTML body are present, the cleaned HTML is preferred; the plain-text copy is
discarded to avoid duplication.

**Why:** Reduces token count, removes visual noise, and focuses the AI on the message content rather than markup.

---

### 5. Separate text/HTML body handling and relevant header extraction

**Upstream behaviour:** The `{{.Content}}` template variable contained only the plain-text body.

**Fork behaviour:**
- Both `TextBody` and `HtmlBody` are extracted from each MIME message.
- The template variable is now `{{.Body}}` (the most informative body part after HTML→Markdown conversion).
- A new `{{.Headers}}` variable exposes selected authentication and routing headers
  (`Received`, `DKIM-Signature`, `Authentication-Results`, `X-Spam-Status`, etc.).

**Why:** Giving the AI routing and authentication headers significantly improves phishing and spoofed-sender
detection without inflating the prompt with irrelevant MIME headers.

---

### 6. Split system/user prompt (`system_prompt` / `user_prompt`)

**Upstream behaviour:** A single `prompt` key combined persona and email data into one string.

**Fork behaviour:** Two optional keys are now supported:
- `system_prompt` — passed as the `system` role message; sets the AI persona.
- `user_prompt`   — passed as the `user` role message; carries the email data via Go template variables.

The legacy `prompt` key is still accepted and maps to `user_prompt` for backward compatibility.

**Why:** Modern chat-completion APIs (OpenAI, Gemini) differentiate between system and user turns.  Separating them
allows clearer instructions and better model compliance.

---

### 7. LLM sampling parameters (`temperature`, `top_p`, `max_tokens`)

**Upstream behaviour:** No way to tune the model's sampling behaviour.

**Fork behaviour:** All AI providers accept three optional parameters in their `config` block:

| Key           | Description                              |
|---------------|------------------------------------------|
| `temperature` | Sampling temperature (0.0–2.0)           |
| `top_p`       | Nucleus sampling probability (0.0–1.0)   |
| `max_tokens`  | Maximum tokens / output tokens in reply  |

**Why:** Allows operators to make the classification more deterministic (low temperature) or to cap response length
and avoid runaway token usage.

---

### 8. Gemini provider (`provider/gemini.go`)

**Upstream behaviour:** Only OpenAI, Ollama, and SpamAssassin were supported.

**Fork behaviour:** Google Gemini is now a first-class provider (`type: gemini`).  It uses the `google.golang.org/genai`
SDK and supports the same `system_prompt`, `user_prompt`, and sampling-parameter options as the other AI providers.

---

### 9. Provider health checks (`HealthCheck()`)

**Upstream behaviour:** No pre-flight connectivity check.

**Fork behaviour:** Each provider implements a `HealthCheck` method that verifies TCP reachability (or config
validity for Ollama) before the application begins processing mail.  Health checks are run at startup.

---

## Configuration changes

### `config.example.yml`

| Field / Key                            | Upstream          | Fork                                   | Notes                                               |
|----------------------------------------|-------------------|----------------------------------------|-----------------------------------------------------|
| `providers.<name>.concurrency`         | absent            | added (`int`, default `1`)             | Worker pool size for IDLE dispatcher                |
| `providers.<name>.rate_limit`          | absent            | added (`float`, default `0`)           | Token-bucket limiter (calls/second)                 || `providers.<name>.config.prompt`       | present           | deprecated, still works                | Replaced by `system_prompt` + `user_prompt`         |
| `providers.<name>.config.system_prompt`| absent            | added (`string`, optional)             | System-role persona for AI providers                |
| `providers.<name>.config.user_prompt`  | absent            | added (`string`, optional)             | User-role email-data template (Go template)         |
| `providers.<name>.config.temperature`  | absent            | added (`float`, optional)              | AI sampling temperature                             |
| `providers.<name>.config.top_p`        | absent            | added (`float`, optional)              | AI nucleus sampling                                 |
| `providers.<name>.config.max_tokens`   | absent            | added (`int`, optional)                | AI max response tokens                              |
| Prompt template variable `{{.Content}}`| present           | renamed to `{{.Body}}`                 | Now contains cleaned HTML (Markdown) or plain text  |
| Prompt template variable `{{.Headers}}`| absent            | added                                  | Selected authentication/routing headers             |
| `inboxes[].enable_idle`                | absent            | added (`bool`, default `false`)        | Switch to IMAP IDLE mode                            |
| `inboxes[].idle_timeout`               | absent            | added (`duration`, default `25m`)      | IDLE keep-alive re-issue interval                   |
| `inboxes[].max_retries`                | absent            | added (`int`, default `3`)             | Retry limit for failed analysis jobs                |

---

## Bug fixes applied to this fork

| File                          | Issue                                                                       | Fix                                                  |
|-------------------------------|-----------------------------------------------------------------------------|------------------------------------------------------|
| `checkpoint/checkpoint.go`    | Missing `logx` import — code compiled but would panic at runtime on debug log calls | Added `github.com/dominicgisler/imap-spam-cleaner/logx` import |
| `provider/provider.go` + `provider/ollama.go` | `checkTCP` was defined in both files — package would not compile | Removed duplicate definition from `ollama.go`; canonical definition kept in `provider.go` |
| `provider/openai.go`          | Missing `"time"` import — `HealthCheck` references `time.Second`            | Added `"time"` import                                |
| `provider/gemini.go`          | Missing `"time"` import — `HealthCheck` references `time.Second`            | Added `"time"` import                                |
| `inbox/inbox.go`              | `jobs` variable used but never declared — package would not compile         | Added `jobs := 0` before the loop                    |
| `inbox/inbox.go`              | Type mismatch: `m.UID` (`goimap.UID`) compared/assigned to `uint32`         | Cast `m.UID` to `uint32` in the comparison           |
| `docs/providers/gemini.md`    | Field descriptions said "OpenAI API key" / "OpenAI model" (copy-paste error)| Corrected to "Google AI Studio / Gemini API key" etc.|

---

## Proposed future improvements

1. ~~**Per-provider worker queues** — implemented in `dispatcher/`.~~ ✅ Done.

2. **Cron-path concurrency** — The cron-scheduled `processInbox` path is still single-threaded.  Multiple inboxes
   scheduled at the same second will execute one after the other.  Consider running each scheduled inbox in its own
   goroutine (with appropriate rate limiting).

3. **Checkpoint directory configuration** — The checkpoint directory is hard-coded as `"checkpoints"` relative to the
   working directory.  Expose it as a top-level config option so Docker deployments can bind-mount a persistent
   volume to a known path.

4. **`maxage` is unused in the new UID-based path** — The original code filtered by message date; the new
   checkpoint-based path fetches all UIDs above the last checkpoint regardless of age.  Either document this
   change clearly or reintroduce the age filter as a secondary guard.

5. **Whitelist regex compilation** — Whitelists are stored as `[]regexp.Regexp` in the config struct, meaning the
   YAML unmarshaller must construct them.  A marshalling failure (invalid regex) is caught by the validator, but the
   error message is not user-friendly.  Consider storing them as strings and compiling lazily with a better error.

6. **Dispatcher for cron path** — The `Dispatcher` is only created for providers used by IDLE inboxes.
   The cron path creates a fresh local provider instance per run.  Consider sharing the dispatcher in the cron
   path too so that `concurrency` and `rate_limit` are respected there as well.

7. **Structured logging** — The `logx` wrapper around logrus uses free-form format strings.  Adopting structured
   fields (e.g. `logrus.WithField`) would make log aggregation and filtering easier in production.
