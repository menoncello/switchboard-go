---
title: 'Config rejeita valores inválidos e aceita usage_url e usage_cache_ttl no YAML'
type: 'bugfix'
ticket: '5'
created: '2026-11-14'
status: 'done'
baseline_revision: '5d9dfa8734559c7c9506e9ea0af7d3ea04978e25'
route: 'oneshot'
route_source: 'auto'
risk: 'low'
review: 'quick'
review_source: 'pinned'
lenses_ran: [quick]
review_loop_iteration: 0
followup_review_recommended: false
context: []
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** `MAX_REQUEST_BODY_BYTES` e `SMTP_PORT` inválidos (não numéricos, `<= 0`, porta fora de 1–65535), pela variável de ambiente ou pelo YAML, são ignorados em silêncio e a partida segue com outro valor, ao contrário das durações, que `validateConfig` recusa. E `usage_url`/`usage_cache_ttl` só existem como variável de ambiente (`USAGE_URL`, `USAGE_CACHE_TTL`).

**Approach:** Seguir o padrão das durações em `config.go`: valor de ambiente inválido vira sentinela que `validateConfig` recusa com mensagem que nomeia o campo (nome da variável e chave YAML); `mergeConfig` deixa passar valor negativo/fora de faixa do YAML para o mesmo `validateConfig`. `yamlConfig` ganha `usage_url` e `usage_cache_ttl` no nível de topo (como `usage_file`), duração Go com `-1` como "ausente", e a variável de ambiente continua por cima. `docs/operations.md` e `docs/configuration.md` citam os dois campos.

</intent-contract>

## Tasks & Acceptance

**Acceptance Criteria:**
- Given `MAX_REQUEST_BODY_BYTES=abc`, `=0` ou `=-5`, when o proxy parte, then `loadConfig` falha com erro que contém `MAX_REQUEST_BODY_BYTES`.
- Given `limits.max_request_body_bytes: -5` no YAML, when o proxy parte, then falha com erro que contém `max_request_body_bytes`.
- Given `SMTP_PORT=abc`, `=0` ou `=70000`, ou `smtp.port: 70000`/`-1` no YAML, when o proxy parte, then falha com erro que contém `SMTP_PORT` (env) ou `smtp.port` (YAML); sem `SMTP_PORT` a porta segue 25.
- Given `usage_url: "https://u"` e `usage_cache_ttl: "5s"` no YAML, when `loadConfig`, then `cfg.UsageURL == "https://u"` e `cfg.UsageCacheTTL == 5s`; com `USAGE_URL`/`USAGE_CACHE_TTL` também definidos, os valores do ambiente valem; `usage_cache_ttl: "0s"` vale 0; `usage_cache_ttl` inválido ou negativo recusa a partida com erro que contém `usage_cache_ttl`.
- Given config válida sem nenhum desses campos, when o proxy parte, then os padrões continuam (20 MiB, porta 25, URL e TTL de uso padrão) e os testes existentes passam.

## Implementation Notes

Oneshot: cerca de 30 linhas em `config.go` (`yamlConfig`, `loadYAMLConfig`, `mergeConfig`, `applyEnvOverrides`, `validateConfig`), testes de config em `main_test.go` no padrão de `TestLoadConfigRejectsInvalidRetryExhaustedAfterEnv` e `TestLoadConfigUsageFileAndTimezone`, e duas linhas de doc. Não muda o formato da config de tiers nem os nomes de variáveis existentes.

Feito:
- `config.go`: env inválido de `MAX_REQUEST_BODY_BYTES` e `SMTP_PORT` vira sentinela `-1`; `mergeConfig` passa adiante body negativo do YAML (`!= 0`); `validateConfig` recusa porta `< 0` ou `> 65535`, com mensagem que cita a variável e a chave YAML. Porta 0 numa `Config` montada à mão continua aceita (testes existentes montam assim); por `loadConfig` o padrão é 25 e 0 vindo do env é recusado.
- `yamlConfig` ganhou `usage_url` e `usage_cache_ttl` no topo; TTL inválido ou negativo no YAML falha em `loadYAMLConfig` com `usage_cache_ttl` na mensagem.
- `main_test.go`: 5 testes novos, um deles confirma que `usage_url` do YAML é consultado por `/admin/keys` e que o `usage_cache_ttl` segura a segunda consulta.
- Docs: `docs/operations.md` e a tabela e o exemplo de `docs/configuration.md`.
- Limite (resolvido no code review): `limits.max_request_body_bytes: 0` e `smtp.port: 0` no YAML eram lidos como "ausente"; os campos viraram ponteiro e `0` recusa a partida.

## Plan Change Log

## Review Triage Log

### 2026-11-14 — Review pass
- verdicts: 4 findings — high 0, medium 0, low 4, false 0, maybe-false 0
- findings:
  - `[low]` `[patch]` Docs diziam que todo valor fora de faixa recusa a partida, mas `0` no YAML de `max_request_body_bytes`/`smtp.port` vale o padrão — docs/configuration.md e docs/operations.md agora dizem que `0` no YAML mantém o padrão
  - `[low]` `[patch]` docs/admin-api.md só citava `USAGE_URL`/`USAGE_CACHE_TTL` — passou a citar `usage_url` e `usage_cache_ttl` também
  - `[low]` `[patch]` Mensagem `USAGE_CACHE_TTL must be >= 0` não nomeava a chave YAML, diferente das novas — virou `USAGE_CACHE_TTL (usage_cache_ttl) must be >= 0`
  - `[low]` `[reject]` Testes novos não limpam `USAGE_URL`/`SMTP_PORT` etc. do ambiente — é o padrão dos testes de config existentes (`TestLoadConfigUsageFileAndTimezone`); só falha com essas variáveis exportadas no shell do teste, e isolar pede um helper novo para todos

## Verification

**Commands:**
- `go test ./...` -- expected: ok
- `go vet ./... && gofmt -l .` -- expected: sem saída

## Auto Run Result

- **Mudança:** `MAX_REQUEST_BODY_BYTES` e `SMTP_PORT` inválidos (env ou YAML) recusam a partida com mensagem que nomeia variável e chave YAML; `usage_url` e `usage_cache_ttl` passam a valer no YAML, com o ambiente por cima.
- **Arquivos:** `config.go` (sentinelas no env, `mergeConfig`, `validateConfig`, campos YAML novos), `main_test.go` (5 testes de config, um de ponta a ponta por `/admin/keys`), `docs/configuration.md`, `docs/operations.md`, `docs/admin-api.md`.
- **Review:** 3 patches (low), 0 deferidos, 1 rejeitado (isolamento de env nos testes, mesmo padrão dos testes existentes).
- **Revisão de acompanhamento:** não recomendada (patches: high 0, medium 0, low 3).
- **Verificação:** `go test ./...` ok, `go vet` e `gofmt -l` limpos, antes e depois dos patches.
- **Riscos residuais:** `0` explícito no YAML para `max_request_body_bytes` e `smtp.port` continua lido como ausente (documentado).


## Code Review

### 2026-11-14

**Code review complete.** 1 `decision-needed`, 4 `patch`, 0 `defer`, 9 rejected.

- [x] [Review][Patch] `0` explícito no YAML para `max_request_body_bytes`/`smtp.port` recusa a partida: `*int64`/`*int` em `yamlConfig`, como `MinContext` (decisão do Eduardo, opção a) [config.go:213]
- [x] [Review][Patch] Faixa de `max_request_body_bytes`/`smtp.port` do YAML checada só depois do env (env válido esconde YAML inválido; mensagem única não diz a origem) — validar em `loadYAMLConfig`, como as durações [config.go:361]
- [x] [Review][Patch] Faltam testes: `smtp.port`/`max_request_body_bytes` válidos pelo YAML, `SMTP_PORT` com espaços, portas 1 e 65535 [main_test.go:3534]
- [x] [Review][Patch] Testes novos não limpam `SMTP_PORT`/`MAX_REQUEST_BODY_BYTES`/`USAGE_URL`/`USAGE_CACHE_TTL` do ambiente (`t.Setenv(x, "")` resolve sem helper novo) [main_test.go:3557]
- [x] [Review][Patch] `TestUsageURLFromYAMLReachesUsageLookup` não prova que o TTL do YAML chega ao cache (o padrão de 60s dá o mesmo resultado) [main_test.go:3606]

**Rejected:**
- `usage_url` sem validação de URL (2 achados) — low: a falha já era a mesma com `USAGE_URL` e aparece logo em `usage_error`; uma URL sem esquema falha em `http.NewRequest` e nenhum token sai.
- Falta nota de quebra de compatibilidade — low: as docs já dizem que valor inválido para a partida, e o erro nomeia o campo.
- `mergeConfig` de `SMTP.Port` fora do diff — false: config.go:396 já usa `!= 0`.
- Texto do plano desatualizado — low: cosmético, num registro de build.
- Erros de `usage_cache_ttl` no YAML sem contexto e com checagem duplicada — false: segue o padrão de `retry_exhausted_after`, e a checagem é necessária por causa da sentinela `-1`.
- `mergeConfig` com `src` montado à mão zera o TTL — false: o único chamador de produção recebe `-1`, e os testes não leem o TTL.
- `validateConfig`, `readyz` e `validate-config` sem teste direto — false: todos recebem a `Config` de `loadConfig`.
- `/status` sem teste com os valores do YAML — false: usa o mesmo lookup de `/admin/keys` (adminkeys.go:155).
