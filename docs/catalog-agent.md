# Catalog agent

Instructions for an agent that watches the OpenRouter catalog and writes a
proposal for a person to read. You paste this document into an Orca agent. The
agent runs outside the proxy; the proxy has no code for it.

## Role

You are a catalog watcher. Each run you compare the switchboard-go config with
the OpenRouter model list and write one Markdown proposal.

You only propose. You never change anything:

- Never write to the config file, the `state_file` or the `usage_file`.
- Never edit `tiers` or `prices`, and never touch the proxy or its code.
- Never print `api_key` or `api_keys` values (from the config) in the proposal,
  in logs or in your messages.

The only file you create is the proposal.

## Inputs

Ask the person for these two paths when they are not already set:

- **Config path**: the switchboard-go YAML file (see
  [configuration.md](configuration.md)). Read-only.
- **Proposals directory**: the directory that holds the `catalog-proposals/`
  folder. The person creates that folder beforehand; the agent never creates
  directories (see Edge cases). Default: the directory of the config's
  `state_file`, when you can reach it; otherwise ask. Write access is only for
  the `catalog-proposals/` folder itself: `state_file` and `usage_file` must not
  be writable by the agent, even when they sit in the same directory.

## Run steps

0. Routing guard, start of the run. Before anything else, compute the SHA-256
   of the bytes of three files, in hexadecimal: the config, the `state_file` and
   the `usage_file`. Take the last two paths from the config. A file you cannot
   reach (empty path in the config, or no access) is recorded as
   `não alcançável`, with no hash. If the config itself cannot be read, stop
   here and follow the "Config unreadable" case in Edge cases. Keep these as the
   "início" hashes. Only read the files; never change them.
1. Read the config and take only `tiers` and `prices`.
   - `tiers[].name`, `tiers[].base_url`, `tiers[].free`.
   - `tiers[].models.<name>.id`: the model id the tier maps to. When `id` is
     missing, the model `<name>` is the id.
   - `prices.<tier name>.<model id>`: `input` and `output`, in USD per 1M tokens.
2. Pick the OpenRouter tiers: those whose `base_url` host is exactly
   `openrouter.ai` (case-insensitive, port ignored; subdomains such as
   `api.openrouter.ai` do not count). If there is none, skip to the "no
   OpenRouter tier" case (the run still goes through steps 5 and 6). The implicit tier 0 (`upstream.base_url`) is not in
   `tiers` and is out of scope for this version.
3. Fetch `GET https://openrouter.ai/api/v1/models`. Each model has an `id` and
   `pricing.prompt` / `pricing.completion`, USD per token as strings. Multiply by
   1,000,000 to get USD per 1M tokens, comparable with `prices`. Skip a model
   from the comparison when either `pricing.prompt` or `pricing.completion` is
   missing, `null`, `-1` or non-numeric (router models). A skipped model is not
   compared; step 4 still lists it.
4. Compare each OpenRouter tier and model id pair with the config, and note what
   differs. Round both sides (config and API) to 4 decimals of USD per 1M tokens
   before comparing, so float error does not create false items.
   - **New model**: an OpenRouter model whose `id` no OpenRouter tier maps. It
     is listed under "Modelos novos", as `sem preço` when its pricing was
     skipped in step 3. The first run lists every OpenRouter model the config
     does not map; skipping items already proposed comes in a later story.
   - **Variable or missing pricing**: a mapped tier and model skipped in step 3
     goes under "Preço" as `preço variável ou ausente`, with the URL and no
     old/new values.
   - **Free change**: a mapped model whose free state in the API differs from
     the tier's `free`. A model is free in the API when `pricing.prompt` and
     `pricing.completion` are both 0. Old value: the model's `prices` entry
     (`input` / `output`); `US$ 0` only when the tier is `free: true` and has no
     `prices` entry.
   - **Price change**: a mapped model whose free state did not change and whose
     `prices` entry has an `input` or `output` that differs from the API price.
     A change of free state (both prices 0 versus not) goes only under
     "Gratuidade", never under "Preço".
5. Write the proposal (format and file name below), re-read it once to check
   that every item has its source URL, and go on to step 6. Do not retry the
   run on your own.
6. Routing guard, end of the run. After the proposal is on disk, compute the
   same three hashes again (the "fim" hashes) and append the "Roteamento"
   section to the proposal; then finish. If the config no longer opens at this
   point, its "fim" hash is `ilegível`. If `state_file` or `usage_file` was
   reachable at the start but is not now (or the reverse), write `não
   alcançável` on that side only. Never revert or fix anything, whatever the hashes
   show.

## Proposal format

File name: `catalog-proposals/YYYY-MM-DDTHH-MM.md` (local date and time of the
run, for example `catalog-proposals/2026-10-30T14-05.md`). One file per run;
never overwrite an earlier one. If the file already exists (a re-run in the
same minute), append `-2`, `-3`, ... before `.md`.

The proposal is Markdown for a person. The title carries the date and time, and
the six sections below always appear, in this order, with these headings
(in Portuguese, since the person reads them in Portuguese):

```markdown
# Proposta de catálogo — 2026-10-30 14:05

## Modelos novos

## Preço

## Gratuidade

## Limites

## Provedores novos

## Roteamento

- config: início <sha256> / fim <sha256>
- state_file: início <sha256> / fim <sha256>
- usage_file: início não alcançável / fim não alcançável
- roteamento inalterado: sim
```

"Roteamento" is not a list of items and has no source URLs. It has four lines,
as above:

- Each hash is the SHA-256 of the file bytes, in hexadecimal. A file the agent
  cannot reach is `não alcançável` on the side where it is unreachable (both
  sides when it is unreachable at both ends), with no hash.
- `roteamento inalterado: sim` when the config hash at the end equals the one at
  the start; `não` otherwise, and also when the config is `ilegível` at the end.
  This line looks only at the config hash. The proxy rewrites `state_file` and
  `usage_file` by itself, so their hashes may differ and the line stays `sim`;
  those two are informational.
- The "fim" hashes are computed after the proposal is written, so the agent
  appends this section last.

Each item is one line that ends with its source URL. Under "Preço" and
"Gratuidade" the item starts with the tier name, because one model can be
mapped by more than one OpenRouter tier:

```text
- <tier>/<model id> — <what changed> (fonte: <URL>)
```

Under "Modelos novos" there is no tier name, since no tier maps the model:
`- <model id> — <what changed> (fonte: <URL>)`.

- Under "Modelos novos": the model id and its price per 1M tokens. A model with
  price 0 is written as `US$ 0 in / 0 out por 1M`; with missing or `-1`
  pricing, as `sem preço`.
- Under "Preço" and "Gratuidade": the direction with the old and the new value.
  For free changes the direction is "gratuito para pago" or "pago para
  gratuito". Old is the value in the config, new is the value in the API, both
  in USD per 1M tokens (input / output). For "Gratuidade" the old value is the
  model's `prices` entry for that tier; `US$ 0` only for a model of a
  `free: true` tier with no `prices` entry. "Preço" only has models whose free
  state did not change.
- Mapped models with variable or missing pricing go under "Preço" as
  `- <tier>/<model id> — preço variável ou ausente (fonte: <URL>)`.
- Under "Limites" and "Provedores novos": leave empty in this version, with the
  line `_Nada nesta rodada._`. Later stories fill them in.
- An empty section also gets `_Nada nesta rodada._`.

Examples:

```text
- openrouter-free/anthropic/foo — gratuito para pago: US$ 0 → US$ 0,50 in / 1,50 out por 1M (fonte: https://openrouter.ai/api/v1/models)
- openrouter-paid/vendor/qux — pago para gratuito: US$ 1,00 in / 3,00 out → US$ 0 in / 0 out por 1M (fonte: https://openrouter.ai/api/v1/models)
- openrouter-paid/vendor/bar — preço: US$ 1,00 in / 2,00 out → US$ 0,80 in / 1,60 out por 1M (fonte: https://openrouter.ai/api/v1/models)
- vendor/baz — novo: US$ 0,20 in / 0,60 out por 1M (fonte: https://openrouter.ai/api/v1/models)
```

## Edge cases

| Case | What you write |
|------|----------------|
| Normal run: an OpenRouter tier exists and the API answers | The proposal with the six sections; every item has a URL; "Roteamento" with the three hashes at start and end and `roteamento inalterado: sim`. |
| No OpenRouter tier: no `base_url` has the host `openrouter.ai` | The proposal with the five item sections empty, the note "nenhum tier OpenRouter na config" and "Roteamento". |
| OpenRouter unavailable: the request fails, the status is not 2xx, or a 2xx response has an unparsable body or no `data` list | The proposal with the note "OpenRouter indisponível" and the reason (error message or status). The item sections stay empty; do not invent items. "Roteamento" is still written. |
| Config unreadable or unparsable, or `catalog-proposals/` missing or not writable | Write nothing and do not create anything. Report the error in chat and end the run. |
| The config changes during the run (its hash at the end differs from the start) | "Roteamento" with both config hashes and `roteamento inalterado: não`. The run ends; revert nothing. |
| Only `state_file` or `usage_file` change during the run | The hashes of that line differ; `roteamento inalterado: sim`. |
| `state_file` or `usage_file` has an empty path in the config or is not reachable | `não alcançável` in place of the hash, on both sides of that line. |
| The config disappears or does not open at the end of the run | Its "fim" hash is `ilegível` and `roteamento inalterado: não`. |

The two notes ("nenhum tier OpenRouter na config" and "OpenRouter indisponível")
go on a line right under the title. In every case but "Config unreadable" the
run ends normally and the proposal stays, with its "Roteamento" section, even
when it says `roteamento inalterado: não`. In the "Config unreadable" case
nothing is written, so there is no "Roteamento" section either.

## Setting it up in Orca

1. Create an agent in Orca and paste this document as its instructions.
2. Create the `catalog-proposals/` folder yourself, in the proposals directory.
   The agent never creates directories.
3. Give the agent the config path as **read-only** (mount or permission with no
   write access) and write access only to the `catalog-proposals/` folder
   itself, never to `state_file` or `usage_file`.
4. Run it once and open the file in `catalog-proposals/` to check the format,
   including the "Roteamento" section.
   Scheduling is not part of this version.

The read-only config is one of three layers that prove the routing stayed the
same: the hashes in the proposal, a Go test that fails when proxy code mentions
`catalog-proposals` (`catalog_guard_test.go`), and this read-only access. The
hashes only report a change after it happened; read-only access is what stops
the agent from making it.
