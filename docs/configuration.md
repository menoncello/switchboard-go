# Configuration

Switchboard Go supports YAML config files and environment variables.

Configuration precedence, from lowest to highest:

1. Built-in defaults
2. YAML config file
3. Environment variables

This lets you keep normal settings in a config file while injecting secrets with
environment variables, systemd environment files, Docker secrets, or your
deployment platform.

## Config file discovery

Switchboard Go looks for a config file in this order:

1. `SWITCHBOARD_GO_CONFIG`, if set
2. `~/.config/switchboard-go/config.yaml`, if it exists
3. `/etc/switchboard-go/config.yaml`, if it exists
4. No config file

If `SWITCHBOARD_GO_CONFIG` is set, the file must exist and be valid YAML.
Missing or invalid explicit config files are startup errors.

Recommended user config path:

```text
~/.config/switchboard-go/config.yaml
```

Recommended system config path:

```text
/etc/switchboard-go/config.yaml
```

Use restrictive permissions, such as `0600`, for config files containing
secrets.

## YAML example

```yaml
server:
  listen_addr: "127.0.0.1:8080"
  proxy_api_key: "replace-with-a-long-random-local-key"

upstream:
  base_url: "https://opencode.ai/zen/go/v1"
  api_keys:
    - "sk-first"
    - "sk-second"
    - "sk-third"
  # How long an exhausted key waits before it is retried automatically.
  # Accepts a Go duration ("30s", "5m", "1h"). "0" disables the cooldown so
  # exhausted keys are retried on the very next request.
  retry_exhausted_after: "5m"
  # Max wait for response headers from the upstream API. LLM completions can
  # spend a long time before sending their first body bytes, so slow
  # non-streaming requests may need this raised. "0" disables the timeout.
  response_header_timeout: "30s"

smtp:
  host: "smtp.example.com"
  port: 587
  username: "alerts@example.com"
  password: "your-smtp-password"
  from: "alerts@example.com"
  to: "you@example.com"
  tls: false
  starttls: true

limits:
  max_request_body_bytes: 20971520

tiers:
  - name: "free-provider"
    base_url: "https://api.example.com/v1"
    api_key: "sk-tier"
    free: true
    styles: ["openai"]
    models:
      model-a:
        id: "provider/model-a"
        context: 131072
        tools: true
```

## Tiers

`tiers:` lists extra upstreams used for failover. The `upstream:` block above
stays as the implicit tier 0 and is not listed here (it is exempt from the
`free` rule). Without `tiers:` the proxy behaves as before. Tiers are read from
the YAML file only, not from environment variables.

When every tier 0 key is exhausted, the proxy tries the tiers in order. It
rewrites the top-level `model` of the request body to the tier's model `id` (the
model name when `id` is empty) and sends it with the tier's `api_key`. A tier
without a mapping for that model, or that does not speak the request's API style,
is skipped, and so is a tier that answers with a quota 429. A request without a
`model` in the body stays on tier 0. Any other response from a tier (any status)
goes to the client without further failover, and a connection error to a tier
returns `502`. If no tier serves the request, the client gets the local `429`
"all upstream keys exhausted"; the SMTP "all exhausted" alert and `Retry-After`
only fire in that case. To try it by hand, run
`go run ./cmd/fakeupstream -addr 127.0.0.1:9999` and switch it with
`POST /__mode/quota` or `POST /__mode/ok`.

| Field | Required | Description |
| --- | --- | --- |
| `name` | Yes | Unique tier name, used in error messages. |
| `base_url` | Yes | Tier base URL (only checked for being non-empty). |
| `api_key` | Yes | Tier key. Never printed by `validate-config` or in logs. |
| `free` | Yes | Must be `true`. A tier without `free: true` is rejected at startup. |
| `styles` | No | List of `openai` and/or `anthropic`. Absent means OpenAI only. |
| `models` | Yes | Map of model name to an object with optional `id` (defaults to the model name), `context` (>= 0) and `tools`. At least one; names must be unique. |

`validate-config` shows only the number of tiers (`tiers=N`).

## Request log

Every proxied request writes one JSON line to stderr when its response ends
(for a stream, after the last event):

```json
{"time":"...","level":"INFO","msg":"request","model":"glm-5.1","api_style":"openai","tier":"opencode-go","key_index":0,"key_name":"main","status":200,"latency_ms":412}
```

- `model`: top-level `model` of the request body, empty if there is none.
- `api_style`: `openai` or `anthropic`.
- `tier`: `opencode-go` for tier 0, the tier `name` for an extra tier, empty when
  no tier answered (local `429`, `502` on a connection error).
- `key_index` and `key_name`: only for tier 0 (`key_name` can be empty).
- `status`: the status the client received.
- `latency_ms`: from before the body is read to the last byte sent to the client.

The key value, `Authorization`, `x-api-key` and the request body and headers are
never logged.

## Environment variables

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `SWITCHBOARD_GO_CONFIG` | No | | Explicit YAML config path. |
| `PROXY_API_KEY` | Yes\* | | API key clients must use to access this proxy. |
| `OPENCODE_GO_API_KEYS` | Yes\* | | Comma-separated OpenCode Go API keys. |
| `LISTEN_ADDR` | No | `:8080` | HTTP listen address. Use `127.0.0.1:8080` for local-only access. |
| `UPSTREAM_BASE_URL` | No | `https://opencode.ai/zen/go/v1` | OpenCode Go upstream base URL for OpenAI-compatible and Anthropic Messages-compatible routes. |
| `OPENCODE_GO_KEY_NAMES` | No | | Comma-separated labels for the keys, in the same order as `OPENCODE_GO_API_KEYS`. Shown by `/admin/keys` so a key is identified by name and index, never by value. Missing or shorter list means empty names. Max 40 characters each; line breaks are removed. |
| `USAGE_URL` | No | `https://opencode.ai/zen/go/v1/usage` | OpenCode Go usage endpoint queried by `/admin/keys` (override for tests). |
| `USAGE_CACHE_TTL` | No | `60s` | How long `/admin/keys` reuses each key's usage lookup, so a refreshing dashboard does not hammer the upstream. Go duration. `0` disables the cache. |
| `MAX_REQUEST_BODY_BYTES` | No | `20971520` | Maximum request body size. Requests above this return `413`. |
| `RETRY_EXHAUSTED_AFTER` | No | `5m` | Cooldown before an exhausted key is retried automatically. Go duration (`30s`, `5m`). `0` disables the cooldown (retry on the next request). Maps to `upstream.retry_exhausted_after`. |
| `UPSTREAM_RESPONSE_HEADER_TIMEOUT` | No | `30s` | Max wait for response headers from the upstream API. Go duration (`30s`, `5m`). `0` disables it. Maps to `upstream.response_header_timeout`. |
| `SMTP_HOST` | No | | SMTP host for notifications. |
| `SMTP_PORT` | No | `25` | SMTP port. |
| `SMTP_USERNAME` | No | | SMTP username. If empty, SMTP AUTH is skipped. |
| `SMTP_PASSWORD` | No | | SMTP password. |
| `SMTP_FROM` | No | | Sender email address. Required for notifications. |
| `SMTP_TO` | No | | Recipient email address. Required for notifications. |
| `SMTP_TLS` | No | `false` | Use implicit TLS when connecting to SMTP. |
| `SMTP_STARTTLS` | No | `false` | Use STARTTLS if the server advertises it. |

\* Required unless provided by YAML config.

Notifications are disabled unless `SMTP_HOST`, `SMTP_FROM`, and `SMTP_TO` are
all set.

## Validate config

Run a config-only check without starting the server:

```bash
switchboard-go validate-config
```

It loads config using normal precedence, validates required values, and prints a
safe summary.

Example env file for systemd:

```bash
PROXY_API_KEY=replace-with-a-long-random-local-key
OPENCODE_GO_API_KEYS=sk-first,sk-second,sk-third
OPENCODE_GO_KEY_NAMES=main,spare,team
SMTP_PASSWORD=replace-me
```
