# SMTP notifications

Switchboard Go can send email when it switches away from an exhausted upstream
key, when all upstream keys have been exhausted, when an extra tier serves a
request tier 0 did not and on the first request a paid tier serves each day (see
"Usage history" in `configuration.md`). The
once-per-day guarantee across restarts needs `usage_file`, and a crash before its
3 s delayed write can repeat the email on restart.

The tier switch email goes out once per extra tier per episode, so a move to
another extra tier warns again. An episode ends when tier 0 serves a request. A
404 model-not-found from tier 0 does not count, since tier 0 is healthy and only
lacks the model. After a failed send the next attempt waits 5 minutes. A restart
starts a new episode, so it may warn again. With no tier 0 key configured, tier 0
never serves and the episode never ends.

Notifications are disabled unless `SMTP_HOST`, `SMTP_FROM`, and `SMTP_TO` are
all set.

Example with STARTTLS:

```bash
export SMTP_HOST="smtp.example.com"
export SMTP_PORT="587"
export SMTP_USERNAME="alerts@example.com"
export SMTP_PASSWORD="your-smtp-password"
export SMTP_FROM="alerts@example.com"
export SMTP_TO="you@example.com"
export SMTP_STARTTLS="true"
```

Equivalent YAML:

```yaml
smtp:
  host: "smtp.example.com"
  port: 587
  username: "alerts@example.com"
  password: "your-smtp-password"
  from: "alerts@example.com"
  to: "you@example.com"
  tls: false
  starttls: true
```

SMTP sending is asynchronous and best effort. Notification failures are logged
but do not fail client requests.
