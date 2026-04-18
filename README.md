# IMAP spam cleaner

![logo](docs/assets/icon_128.png)

[![Latest release](https://img.shields.io/github/v/release/krittykitty/imap-spam-cleaner?style=for-the-badge)](https://github.com/krittykitty/imap-spam-cleaner/releases)
[![License](https://img.shields.io/github/license/krittykitty/imap-spam-cleaner?style=for-the-badge)](https://github.com/krittykitty/imap-spam-cleaner/blob/master/LICENSE)
[![Issues](https://img.shields.io/github/issues/krittykitty/imap-spam-cleaner?style=for-the-badge)](https://github.com/krittykitty/imap-spam-cleaner/issues)

A tool to clean up spam in your IMAP inbox using AI and rule-based providers.

Forked from [dominicgisler/imap-spam-cleaner](https://github.com/dominicgisler/imap-spam-cleaner).

## Summary

This application loads mail from configured IMAP inboxes and checks their contents using a configured provider.
Depending on a spam score, the message can be moved to the spam folder, keeping your inbox clean.

Key enhancements over upstream:

- **Incremental processing** — UID-based checkpointing means only new messages are fetched on each run; no redundant re-scanning.
- **IMAP IDLE** — real-time new-mail detection without relying solely on a cron schedule (`enable_idle` option).
- **Per-provider worker queues** — each provider gets its own bounded, concurrent worker pool with configurable rate limiting and exponential-backoff retries.
- **HTML→Markdown conversion** — HTML email bodies are converted to simplified Markdown before being sent to AI providers, reducing token usage and noise.
- **Separate text/HTML body handling** — the most informative body part is selected automatically.
- **Sent-folder memory** — outgoing recipients are tracked in SQLite so replies can be auto-whitelisted and skip the LLM entirely.
- **Sent-folder memory** — outgoing recipients are tracked in SQLite so replies can be auto-whitelisted and skip the LLM entirely.
// **Recent message memory & consolidation** — (archived; see `archive/` for legacy code)
- **Relevant header extraction** — key authentication and routing headers (`Received`, `DKIM-Signature`, etc.) are forwarded to the AI for better phishing/spoofing detection.
- **Split system/user prompt** — `system_prompt` sets the AI persona; `user_prompt` carries the email data. Both are fully customisable.
- **LLM parameters** — `temperature`, `top_p`, and `max_tokens` are configurable per provider.
- **Gemini provider** — Google Gemini is supported in addition to OpenAI and Ollama.
- **Provider health checks** — TCP reachability is verified before starting to process mail.

## Data Rules

Hard requirements for mailbox data handling:

- **Sent folder (`sent`)**: only `To` recipient addresses are used, and only for the whitelist contact memory.
// **Inbox recent store**: store `from`, `to`, `subject`, and a cleaned plain-text snippet.
- **Snippet limit**: inbox snippets are truncated to **100 bytes**.
// **Analysis enrichment**: inbox recent records include spam score and model reason after analysis.

## Example

```console
$ docker run -v ./config.yml:/app/config.yml krittykitty/imap-spam-cleaner:latest
INFO   [2026-02-28T16:53:41Z] Starting imap-spam-cleaner v0.5.3
DEBUG  [2026-02-28T16:53:41Z] Loaded config
INFO   [2026-02-28T16:53:41Z] Scheduling inbox info@example.com (*/5 * * * *)
INFO   [2026-02-28T16:55:00Z] Handling info@example.com
DEBUG  [2026-02-28T16:55:00Z] Available mailboxes:
DEBUG  [2026-02-28T16:55:00Z]   - INBOX
DEBUG  [2026-02-28T16:55:00Z]   - INBOX.Drafts
DEBUG  [2026-02-28T16:55:00Z]   - INBOX.Sent
DEBUG  [2026-02-28T16:55:00Z]   - INBOX.Trash
DEBUG  [2026-02-28T16:55:00Z]   - INBOX.Spam
DEBUG  [2026-02-28T16:55:00Z]   - INBOX.Spam.Cleaner
DEBUG  [2026-02-28T16:55:00Z] First run for info@example.com: establishing checkpoint (no messages processed this run)
DEBUG  [2026-02-28T16:55:00Z] Checkpoint initialised at UID 477 (UIDValidity=1234567890)
INFO   [2026-02-28T16:56:00Z] Handling info@example.com
DEBUG  [2026-02-28T16:56:00Z] Loaded 5 new messages since UID 477
DEBUG  [2026-02-28T16:56:06Z] Spam score of message #478 (Herzlichen Glückwunsch! Ihr Decathlon-Geschenk wartet auf Sie. 🎁): 90/100
DEBUG  [2026-02-28T16:56:12Z] Spam score of message #479 (Leider ist bei der Verarbeitung Ihrer Zahlung ein Problem aufgetreten.): 90/100
DEBUG  [2026-02-28T16:56:18Z] Spam score of message #480 (Das neue Geheimnis gegen Bauchfett!): 92/100
DEBUG  [2026-02-28T16:56:26Z] Spam score of message #481 (Schnell: 1 Million / Lady Million): 80/100
DEBUG  [2026-02-28T16:56:32Z] Spam score of message #483 (Vermögen x4 zu Fest): 85/100
INFO   [2026-02-28T16:56:32Z] Moved 4 messages
```

## Configuration

See [config.example.yml](config.example.yml) for a full annotated example.

Refer to the [docs/](docs/) directory for detailed documentation on each section.

## Contributors

Feel free to contribute to this project by opening issues for useful features, reporting bugs, or implementing requested features.

<!-- readme: contributors -start -->
<table>
	<tbody>
		<tr>
            <td align="center">
                <a href="https://github.com/dominicgisler">
                    <img src="https://avatars.githubusercontent.com/u/13015514?v=4" width="100;" alt="dominicgisler"/>
                    <br />
                    <sub><b>Dominic Gisler</b></sub>
                </a>
            </td>
            <td align="center">
                <a href="https://github.com/krittykitty">
                    <img src="https://avatars.githubusercontent.com/u/83251536?v=4" width="100;" alt="krittykitty"/>
                    <br />
                    <sub><b>krittykitty</b></sub>
                </a>
            </td>
            <td align="center">
                <a href="https://github.com/nistei">
                    <img src="https://avatars.githubusercontent.com/u/1652722?v=4" width="100;" alt="nistei"/>
                    <br />
                    <sub><b>Niklas Steiner</b></sub>
                </a>
            </td>
            <td align="center">
                <a href="https://github.com/alanpbaldwin">
                    <img src="https://avatars.githubusercontent.com/u/1648277?v=4" width="100;" alt="alanpbaldwin"/>
                    <br />
                    <sub><b>alanpbaldwin</b></sub>
                </a>
            </td>
		</tr>
	<tbody>
</table>
<!-- readme: contributors -end -->

