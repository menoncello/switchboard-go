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

# Telegram alert when no tier can serve a request. Both fields are required.
# The token sits in the file like the other credentials; there are no env vars.
telegram:
  bot_token: "123456:your-bot-token"
  chat_id: "123456789"

limits:
  max_request_body_bytes: 20971520

# Smallest declared context a tier model needs to be eligible (default 64000;
# 0 turns the floor off).
min_context: 64000

# File where tier cooldowns and exhausted tier 0 keys are saved (see below).
state_file: "/var/lib/switchboard-go/state.json"

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

## State file

`state_file` (top-level, YAML only, no environment variable) is the path of a
JSON file where the proxy saves the running tier cooldowns (tier 0 included) and
the tier 0 keys that are exhausted inside their cooldown, so a restart does not
send requests to a tier or key that is still cooling down. The proxy rewrites it
after a request when something changed, writing to a temporary file in the same
directory and renaming it, so the directory must be writable. A key is stored as
a prefix of its SHA-256, never its value, so reordering `api_keys` is safe.
Entries whose cooldown has ended are not restored. With `retry_exhausted_after: 0`
no key is saved, since exhausted keys have no cooldown. A missing or invalid file at
startup is logged and the proxy starts without state; a write failure is logged
and does not affect the request. Without `state_file` nothing is saved and
startup (and `validate-config`) logs a warning. Daily spend is not persisted.

## Tiers

`tiers:` lists extra upstreams used for failover. The `upstream:` block above
stays as the implicit tier 0 and is not listed here (it is exempt from the
`free` rule). Without `tiers:` there is nothing to fail over to: a tier 0 failure status (see below) still puts tier 0 in cooldown, and until it ends the proxy answers the local `429`. Tiers are read from
the YAML file only, not from environment variables.

A request goes to tier 0 first, then to the tiers in order. Each tier that fails
is skipped (put in cooldown) and the next one is tried. A tier fails on `401`,
`402`, any `5xx`, any `429`, and a `404` that is not about a missing model. A
`404` fails the tier only when the request body has a non-empty top-level
`model` and the `404` body does not say the model is missing (it contains "model"
and one of "not found", "does not exist", "unknown", "invalid", "no such");
otherwise it goes to the client. Any other status (`400`, a plain `403`, `2xx`,
`3xx`, other `4xx`) goes to the client without cooldown. Failover happens only
before the response is copied: once a response starts (a stream included) there
is no switch, even if it is cut.

On tier 0 only a quota `429` and the `403` "subscription is required" rotate to
the next key; with every key exhausted tier 0 is skipped as before. Any other
failure status puts the whole of tier 0 in cooldown for `retry_exhausted_after`.
A tier in cooldown, tier 0 included, is not called until the cooldown ends; the
cooldown is lost on restart unless `state_file` is set (see "State file"). For
an extra tier the cooldown is its `cooldown` field or, when absent, `retry_exhausted_after`; `0` means no
cooldown (the next tier is tried only for the request that failed).

For an extra tier the proxy rewrites the top-level `model` of the request body to
the tier's model `id` (the model name when `id` is empty) and sends it with the
tier's `api_key`. A tier without a mapping for that model, or that does not speak
the request's API style, is skipped. A request without a `model` in the body stays
on tier 0. A connection error or timeout (including `response_header_timeout`)
before the first byte counts as a failed attempt: the request goes on to the next
tier, with no cooldown and without rotating keys. A client that cancels the
request ends it: no other tier is called and no state changes (logged as `499`).

If every tier fails, the client gets the last failure response received (its
tier and key are the ones in the request log). If no tier returned a response
and a connection error or timeout happened, the client gets `502`. If no tier was tried at all
(all in cooldown, skipped or tier 0 keys exhausted) the client gets the local
`429` "all upstream keys exhausted", with `Retry-After` set to the shortest time
left among the tiers that could serve the request (tier 0 counts its tier
cooldown or, without one, its exhausted keys); the header is left out when that
time is unknown. The SMTP "all exhausted" alert fires whenever no tier answers (the local `429`, or the last upstream response being served)
and only when every tier 0 key is exhausted. The Telegram alert (`telegram:`)
fires on the first request that no tier serves and stays quiet until some tier
serves a request again, so it is sent once per failure episode; it runs next to
the SMTP alert and sends nothing unless both `bot_token` and `chat_id` are set.
A restart starts armed, so the first failing request after it alerts. If the
send itself fails, the next failing request tries again (not the same one). An
upstream 4xx passed through to the client counts as served and also re-arms.
To try it by hand, run
`go run ./cmd/fakeupstream -addr 127.0.0.1:9999` and switch it with
`POST /__mode/quota` or `POST /__mode/ok`.

| Field | Required | Description |
| --- | --- | --- |
| `name` | Yes | Unique tier name, used in error messages. |
| `base_url` | Yes | Tier base URL (only checked for being non-empty). |
| `api_key` | Yes | Tier key. Never printed by `validate-config` or in logs. |
| `free` | Yes | Must be `true`. A tier without `free: true` is rejected at startup. |
| `styles` | No | List of `openai` and/or `anthropic`. Absent means OpenAI only. |
| `cooldown` | No | Duration (`time.ParseDuration`, for example `10m`, >= 0) the tier is skipped after a failure. Absent means `retry_exhausted_after`; `0` means no cooldown. |
| `models` | Yes | Map of model name to an object with optional `id` (defaults to the model name), `context` (>= 0, the context window in tokens; absent counts as 0) and `tools` (`true` when the model supports tool calling; absent is `false`). At least one; names must be unique. |

`validate-config` shows only the number of tiers (`tiers=N`).

### API styles

A request is `anthropic` when its path starts with `/v1/messages` or
`/v1/complete`, or it carries any `anthropic-*` header; otherwise it is `openai`.
`styles` lists the styles a tier speaks, and the proxy never translates between
them: a tier that does not list the request's style is skipped. The credential
follows the request's style, not the tier's: `x-api-key` (plus
`anthropic-version`, `2023-06-01` when the client sent none) for `anthropic`, and
`Authorization: Bearer` for `openai`, so a tier that lists both authenticates
each request the way that request expects. Tier 0 is not filtered by style. When
no tier speaks the request's style and no tier returned a response or failed
with a connection error or timeout (that case is a `502`), the client gets the
local `429` (see above), in the request's own error format. A provider that
serves the two styles at different URLs is two tiers, each with its own
`base_url`.

### Eligibility

A mapped model only serves a request on its tier when it is eligible:

- `context` must be at least the top-level `min_context` (default `64000`, only
  from the YAML file, no environment variable). A model with no `context` counts
  as `0` and is not eligible. `min_context: 0` turns the floor off; a negative
  value is rejected at startup.
- When the request body has a non-empty `tools`, the model must have
  `tools: true`. A request without `tools` (or with `tools: []` or `null`) does
  not need it. The request is never changed: `tools` is not stripped.

An ineligible tier is skipped like a tier that does not map the model or the
style, and `Retry-After` does not count it. Tier 0 is exempt from both checks.

A request counts as "with tools" only when the top-level `tools` is a non-empty
array. The legacy `functions` field and a `tools` that is not an array are not
treated as tools.

Upgrade note: a tier model that does not declare `context` counts as `0`, so an
existing config whose tier models omit it becomes ineligible under the default
`min_context: 64000`. Declare `context` on those models or set `min_context: 0`.
At startup (and in `validate-config`) the proxy logs a warning for each tier with
no model that reaches `min_context`.

## Request log

Every proxied request writes one JSON line to stderr when its response ends
(for a stream, after the last event):

```json
{"time":"...","level":"INFO","msg":"request","model":"glm-5.1","api_style":"openai","tier":"opencode-go","project":"sem projeto","key_index":0,"key_name":"main","tokens_in":1204,"tokens_out":87,"status":200,"latency_ms":412}
```

- `model`: top-level `model` of the request body, empty if there is none;
  cut at 128 bytes.
- `api_style`: `openai` or `anthropic`.
- `tier`: `opencode-go` for tier 0, the tier `name` for an extra tier, empty when
  no tier answered (local `429`, `502` when no tier was reachable, `499` when the client cancelled).
- `project`: the optional `X-Project` request header, up to 64 characters of
  `[A-Za-z0-9._-]`. Absent, longer or with another character it logs `sem projeto`
  and the request goes on. The proxy never forwards `X-Project` to the upstream.
- `key_index` and `key_name`: only for tier 0 (`key_name` can be empty).
- `status`: the status the client received.
- `latency_ms`: from before the body is read to the last byte sent to the client.
- `tokens_in` and `tokens_out`: the token counts from the `usage` field of the
  upstream response (OpenAI `prompt_tokens`/`completion_tokens`, Anthropic
  `input_tokens`/`output_tokens`), for a JSON body and for a stream. In a stream
  they come from the last `usage` seen (Anthropic: `input_tokens` from
  `message_start`, cumulative `output_tokens` from the last `message_delta`).
  The proxy never changes the request to get them and never estimates them.
- `usage_missing`: `true` when the response was `2xx` from a tier but carried no
  usable `usage` (absent, malformed, a compressed body with `Content-Encoding`,
  or a JSON body above 1 MiB). The line then has no `tokens_*`.
  Responses that are not `2xx` and requests no tier answered (local `429`, `502`,
  `499`, `413`, `400`) have neither `tokens_*` nor `usage_missing`.

Requests rejected while reading the body (`413` too large, `400` read failure)
are logged too, with empty `model` and `tier` and no key fields.

The key value, `Authorization`, `x-api-key` and the request body are never
logged. The only request header logged is `X-Project`, and only as `project`
after validation.

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
