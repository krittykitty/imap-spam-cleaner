# Evaluation Report — Holistic Gap Analysis (since cf8c030)

Date: 2026-04-14
Range: cf8c030..fd8fe96
Scope: full repo evaluation for additions, completeness, quality, and breakage risk since cf8c030.

---

## Summary

- Change volume: 10 commits, 42 changed files, 2827 insertions, 178 deletions.
- Build status: pass.
- Test status: pass after setting a local GOTMPDIR due this host's /tmp execution restriction.
- Lint status: not runnable in this environment through make because golangci-lint is not installed locally.
- Overall: strong feature progress, but there are two material correctness/documentation regressions and several integration/testing gaps.

---

## Verification Executed

- go build ./... -> pass
- go test ./... -v -> fails on this host with fork/exec permission denied from /tmp
- GOTMPDIR=$PWD/.tmp/go-build go test ./... -v -> pass
- GOTMPDIR=$PWD/.tmp/go-build go test ./... -cover -> pass, but low coverage in key new areas
- make lint -> fails locally (golangci-lint binary missing)

Coverage snapshot:

- app: 73.1%
- inbox: 10.2%
- internal/dispatcher: 0.0%
- provider: 8.7%
- storage: 54.3%
- imap: 21.6%

---

## Prioritized Findings

### Critical

1) Prompt contract mismatch can cause analysis parse failures in default/example usage.

- Code now strictly expects JSON responses for AI providers via json.Unmarshal:
	- provider/openai.go:97-100
	- provider/gemini.go:104-107
	- provider/ollama.go:94-97
- Default prompt contract in code also asks for JSON:
	- provider/aibase.go:21
	- provider/aibase.go:39-47
- But shipped example/docs still instruct integer-only output:
	- config.example.yml:16
	- docs/providers/openai.md:40
	- docs/providers/ollama.md:37
- Impact: users copying current examples can provoke provider responses that fail JSON parsing and skip classification.

2) max_retries cannot be set to 0 intentionally (configuration semantic bug).

- In app/config.go:153-155, MaxRetries is forced to 3 whenever value is 0.
- Validation allows 0, so config says 0 is valid but runtime silently changes it.
- Impact: impossible to disable retries explicitly; behavior differs from user intent.

### High

3) initial population feature appears implemented but not wired.

- Function exists at inbox/inbox.go:106.
- No call sites were found in repository search (only declaration match).
- Impact: first-run recent memory bootstrap path is effectively dead code, so intended initial context seeding does not occur.

4) Built binary is committed into repo history.

- Added file: imap-spam-cleaner (binary, ~28MB in diff stat).
- Impact: repository bloat, noisy diffs, potential release artifact confusion in source tree.

### Medium

5) Test coverage is sparse in newly added concurrency/integration paths.

- internal/dispatcher has no tests and 0% coverage.
- No dedicated tests found for sent-folder sync path in inbox/sent_sync.go.
- inbox package overall coverage is 10.2% despite large new logic surface.

6) Lint is not validated by CI in this repo state.

- Local make lint depends on golangci-lint binary (makefile: lint target).
- CI workflow currently runs tests only and no lint step.
- Impact: style/static issues can regress unnoticed.

### Low

7) Documentation quality nits.

- README has duplicated Sent-folder memory bullet.
- storage docs describe only Consolidate(string) interface path, while code now prefers ConsolidateVars first.

---

## Feature Matrix (since cf8c030)

| Feature | Status | Components | Tests | Docs | Blockers | Priority |
|---|---|---|---|---|---|---|
| UID checkpointing & incremental scans | Complete | checkpoint/, imap/, inbox/ | Present | Present | None observed | Low |
| IMAP IDLE loop & trigger flow | Partial | inbox/idle.go, inbox/inbox.go | Basic tests only | Present | limited integration coverage | Medium |
| Per-provider dispatcher concurrency/rate limit/retry | Partial | internal/dispatcher/, inbox wiring, app/config.go | No dispatcher tests | Partial | untested worker/backoff behavior | High |
| Sent-folder contact memory | Partial | inbox/sent_sync.go, storage/storage.go, main.go | storage tests yes, sync path no | Present | no dedicated sent-sync tests | Medium |
| Recent-message memory & consolidation | Partial | storage/recent.go, inbox/inbox.go, provider/* | consolidation unit test exists | Present | initialPopulation not wired | High |
| Prompt system/user split + JSON response schema | Partial | provider/aibase.go, provider/{openai,gemini,ollama}.go | limited | Inconsistent | docs/example prompt mismatch | Critical |
| Consolidation provider overrides | Partial | app/config.go, inbox/inbox.go, provider/aibase.go | limited | partial | mostly untested branch matrix | Medium |
| Provider health checks at startup | Complete | main.go, provider/* | indirect | Present | None observed | Low |
| CI baseline | Partial | .github/workflows/ci.yml | tests only | n/a | no lint gate | Medium |

---

## Environment vs Code Issues

- Environment-specific (not code regression): this machine mounts /tmp without execute permissions for Go test binaries; local workaround is GOTMPDIR in workspace.
- Code/documentation regressions: prompt-output mismatch, max_retries override semantics, unreachable initialPopulation wiring.

---

## Recommended Action Plan

### Immediate

1) Align all shipped prompts/examples/docs to JSON response schema (score, reason, is_phishing).
2) Fix MaxRetries defaulting so explicit 0 remains 0 (for example by differentiating unset from set via pointer or a parse-time presence flag).
3) Either wire initialPopulation into first-run flow or remove the dead code until implemented.

### Short term

4) Add dispatcher unit tests (worker startup, rate limit pacing, retry/backoff, shutdown behavior).
5) Add sent-sync tests covering checkpoint rollover and recipient extraction edge cases.
6) Add lint step to CI and pin golangci-lint version.

### Cleanup

7) Remove committed binary artifact from source control tracking and release through artifacts/tags instead.
8) Resolve doc nits (duplicate README bullet, consolidation interface wording).

---

## Repro Commands

- Build: go build ./...
- Tests (host-safe): mkdir -p .tmp/go-build && GOTMPDIR=$PWD/.tmp/go-build go test ./... -v
- Coverage: mkdir -p .tmp/go-build && GOTMPDIR=$PWD/.tmp/go-build go test ./... -cover
