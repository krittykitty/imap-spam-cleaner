# Storage & Memory

The application stores two per-inbox SQLite databases under the `storage/` directory relative to the working directory. These databases are created automatically when the related features are used.

Files and purpose

- Sent-folder contacts database
  - Location: `storage/sent_contacts__<host>__<username>__<inbox>.db`
  - Purpose: stores normalized recipient addresses extracted from the configured `sent_folder`. Used when `enable_sent_whitelist: true` to auto-whitelist senders who have previously received mail from the configured inbox.

- Recent messages database
  - Location: `storage/recent_messages__<host>__<username>__<inbox>.db`
  - Purpose: stores recent message metadata (uid, from, to, subject, snippet, date, spam score, LLM reason, whitelisted flag and an `updated_at`). It is used to build a recent consolidated context for LLM-based consolidation and for debugging/history purposes.

Persistence

When running in Docker, mount a host directory to persist these databases across container restarts, for example:

```yaml
services:
  imap-spam-cleaner:
    image: dominicgisler/imap-spam-cleaner:latest
    volumes:
      - ./config.yml:/app/config.yml:ro
      - ./storage:/app/storage       # persist sent/ recent DB files
      - ./checkpoints:/app/checkpoints
```

Retention and pruning

- Sent contacts: when `enable_sent_whitelist: true`, the sent-folder sync job will call `PruneOlderThan()` using the configured `sent_folder_maxage` to remove old contacts. See `sent_folder_maxage` in the inbox configuration.
- Recent messages: the recent DB collects message metadata as messages are processed. Consolidation queries only include messages within a configured window (see `recent_consolidation_interval` and `recent_consolidation_every`), but the DB is not automatically pruned by default — use maintenance or implement a scheduled call to `PruneOlderThan` if you want DB file size constraints.

Consolidation

The application stores a short consolidation summary in the recent DB (`consolidations` table). Consolidation is triggered when enough messages have been processed (`recent_consolidation_every`) or when the last consolidation is older than `recent_consolidation_interval`.

If the configured provider implements a `Consolidate(string) (string, error)` method, the provider will be used to produce a condensed summary; otherwise a fallback text summary is stored.

Notes

- DB filenames are sanitized and include the IMAP `host`, `username`, and `inbox` values. If you move or rename the `storage/` directory, the app will create new files using the same naming rules.
- To clear stored memory for an inbox remove the corresponding DB files from the `storage/` directory.
