# Inboxes

| Field          | Type     | Required | Default | Description                                                         | Example           |
|----------------|----------|----------|---------|---------------------------------------------------------------------|-------------------|
| `schedule`     | string   | yes      |         | Cron schedule defining when spam analysis runs (ignored when `enable_idle: true`) | `*/5 * * * *` |
| `host`         | string   | yes      |         | IMAP server hostname                                                | `mail.domain.tld` |
| `port`         | integer  | yes      |         | IMAP port                                                           | `143`             |
| `tls`          | boolean  | no       | `false` | Enable TLS                                                          | `false`           |
| `username`     | string   | yes      |         | IMAP username                                                       | `user@domain.tld` |
| `password`     | string   | yes      |         | IMAP password                                                       | `mypass`          |
| `provider`     | string   | yes      |         | Provider used for spam detection                                    | `prov1`           |
| `inbox`        | string   | yes      |         | Folder to scan                                                      | `INBOX`           |
| `spam`         | string   | yes      |         | Folder where spam messages are moved                                | `INBOX.Spam`      |
| `minscore`     | integer  | yes      |         | Minimum spam score required to classify as spam (0–100)             | `75`              |
| `minage`       | duration | no       | `0h`    | Minimum age of message before scanning                              | `0h`              |
| `maxage`       | duration | no       | `24h`   | Maximum age of message considered                                   | `24h`             |
| `whitelist`             | string   | no       |         | Whitelist to use (empty/missing = no whitelist)                     | `whitelist1`      |
| `enable_sent_whitelist` | boolean  | no       | `false` | Enable sent-folder memory to auto-whitelist senders from outgoing mail | `true`            |
| `sent_folder`           | string   | no       | `Sent`  | Folder to scan for outgoing messages when building the contact memory | `Sent`            |
| `sent_folder_maxage`    | duration | no       | `2160h` | How long to retain sent-folder contacts before pruning              | `2160h`            |
| `sent_folder_schedule`  | string   | no       | `"0 * * * *"` | Cron schedule for periodic sent-folder sync                 | `"0 * * * *"`   |
| `recent_consolidation_every` | integer | no | `50` | Run consolidation after this many processed messages | `50` |
| `recent_consolidation_interval` | duration | no | `24h` | Run consolidation if the last summary is older than this interval | `24h` |
| `enable_idle`           | boolean  | no       | `false` | Use IMAP IDLE for real-time new-mail detection instead of polling   | `true`            |
| `idle_timeout`          | duration | no       | `25m`   | How long to hold an IDLE connection before re-issuing it            | `25m`             |
| `max_retries`           | integer  | no       | `3`     | Maximum retry attempts when provider analysis fails transiently     | `3`               |

```yaml
inboxes:
  - schedule: "*/5 * * * *"
    host: mail.domain.tld
    port: 143
    tls: false
    username: user@domain.tld
    password: mypass
    provider: prov1
    inbox: INBOX
    spam: INBOX.Spam
    minscore: 75
    minage: 0h
    maxage: 24h
    whitelist: whitelist1
    enable_sent_whitelist: true  # enable sent-folder memory to auto-whitelist replies
    sent_folder: Sent            # folder to scan for outgoing messages
    sent_folder_maxage: 2160h    # how long sent contacts are retained (default 3 months)
    sent_folder_schedule: "0 * * * *" # cron schedule for sent-folder sync
    # recent_consolidation_every: 50 # run consolidation after this many processed messages
    # recent_consolidation_interval: 24h # refresh consolidation if older than this interval
    enable_idle: false   # set to true to use IMAP IDLE instead of the cron schedule above
    idle_timeout: 25m    # re-issue IDLE after this duration (keep-alive)
    max_retries: 3       # retry failed analysis jobs up to this many times
```

