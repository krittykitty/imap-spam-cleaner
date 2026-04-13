# Evaluation Report — dispatcher & IDLE wiring (since cf8c030)

Date: 2026-04-14

Scope: verify implementation of the dispatcher and IDLE wiring described in FORK-CHANGES.md, run tests and produce prioritized action items.

---

## Executive summary

- Implemented a per-provider Dispatcher (`internal/dispatcher/dispatcher.go`) offering a bounded worker pool, per-worker provider instances, token-bucket rate limiting, and retry/backoff. API: `Analyze(ctx, msg, maxRetries)` and `Close()`.
- Wired dispatcher into the IMAP IDLE path: `Schedule()` creates dispatchers for IDLE-enabled inbox providers; `StartIdle` / `runIdleSession` / `triggerProcess` forward a `*dispatcher.Dispatcher` to `processInboxInternal`.
- Added config fields: `providers.<name>.concurrency`, `providers.<name>.rate_limit`, and `inboxes[].max_retries` with sensible defaults in `app/config.go`.
- Build succeeded; unit test suite passed when running with a repo-local TMPDIR (`TMPDIR=$(pwd)/tmp go test ./...`).

## Files added / modified

- Added: `internal/dispatcher/dispatcher.go` — worker pool, rate limiter, retry/backoff
- Modified: `app/config.go` — added `Concurrency`, `RateLimit`, `MaxRetries` and defaults
- Modified: `inbox/inbox.go` — `buildDispatchers`, `processInboxInternal`, dispatcher wiring for cron vs IDLE paths
- Modified: `inbox/idle.go` — `StartIdle`, `runIdleSession`, `triggerProcess` accept `*dispatcher.Dispatcher`
- Modified: `inbox/idle_test.go` — updated tests to pass `nil` dispatcher when appropriate

## Status against FORK-CHANGES.md items

- UID checkpointing (`checkpoint/`): ✅ Present & tested
- IMAP IDLE support: ✅ Present & tested
- Per-provider Dispatcher (IDLE path): ✅ Implemented (no unit tests yet) — STATUS: Partial
- HTML→Markdown (`mailclean/`): ✅ Present & tested
- Body/header handling & prompt changes: ✅ Present
- System/user prompt split & LLM sampling params: ✅ Present
- Gemini provider: ✅ Present
- Provider `HealthCheck()`: ✅ Implemented and invoked at startup (`main.go`)

## Test results

- `go build ./...` — OK
- `TMPDIR=$(pwd)/tmp go test ./...` — OK (all package tests passed)

Note: CI environments that mount `/tmp` with `noexec` will reproduce the earlier `fork/exec ... permission denied` error; set `TMPDIR` to an executable path in CI or configure the runner accordingly.

## Observations & risks

- Dispatcher is implemented but currently has no unit tests. This is the largest remaining gap for correctness of concurrency, rate limiting and retry logic.
- `config.example.yml` does not yet demonstrate `concurrency` / `rate_limit` / `max_retries` usage; docs mention them but examples should be kept in sync.
- `internal/dispatcher` uses a buffered `jobCh` and blocks when full; during shutdown callers can block on job submission unless they respect context cancellation. This can be improved for graceful shutdown.

## Prioritized action items

Critical
- Add unit tests for `internal/dispatcher` covering: basic Analyze flow, concurrency (N workers), rate_limit behavior, and retry/backoff semantics. (Short-term)
- Update `config.example.yml` with `concurrency`/`rate_limit` and example `max_retries` in an `inboxes` entry. (Short-term)
- Ensure CI runs tests with an executable TMP (set `TMPDIR=$GITHUB_WORKSPACE/tmp` or similar) to avoid `noexec` failures. (Short-term)

High
- Add an integration test for IDLE + dispatcher (mock IMAP server or simulate Unilateral mailbox signals). (Medium)
- Harden dispatcher Analyze submission to return early when dispatcher is shutting down (non-blocking select with ctx). (Medium)

Medium
- Add documentation snippet in README or docs explaining dispatcher behavior and when to tune `concurrency` / `rate_limit` / `max_retries`. (Medium)
- Consider optional cron-path concurrency changes if desired. (Optional)

Low
- Minor code cleanup and more informative log messages for dispatcher worker lifecycle. (Low)

## How to reproduce & run locally

- Build:

```bash
cd /path/to/imap-spam-cleaner
go build ./...
```

- Run tests (use repo-local TMPDIR to avoid `noexec` mounts):

```bash
cd /path/to/imap-spam-cleaner
TMPDIR=$(pwd)/tmp go test ./...
```

## Next steps I can take (pick any)

- Implement unit tests for `internal/dispatcher` (recommended next step).
- Update `config.example.yml` and docs to reflect the new config keys.
- Add a non-blocking job submit to Dispatcher.Analyze to avoid hangs during shutdown.
- Add integration tests for the IDLE+dispatcher path.

---

If you'd like, I can proceed with implementing the dispatcher unit tests next or update the example config and docs.