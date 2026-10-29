---
title: 'Seção de tiers na página /status'
type: 'feature'
ticket: 8
created: '2026-10-29'
status: 'built'
baseline_revision: 'bbac3e4dbde39d588d109f15a54b78f2532e70ea'
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

**Problem:** A página `/status` mostra só as chaves do tier 0; o Eduardo não vê quais tiers estão em cooldown nem por quanto tempo.

**Approach:** Quando a config tem tiers extras, `/status` ganha, abaixo da tabela de chaves, uma tabela com o tier 0 (OpenCode Go) e cada tier de `tiers:` com estado (`ok` ou `cooldown`) e o tempo restante, lido de `a.cooldowns`. Nada de chave, de URL ou de valor de tier na página; sem tiers extras a página fica como hoje. O uso do dia é do épico de relatório.

</intent-contract>

## Implementation Notes

Oneshot: ~40 linhas em `adminkeys.go` (struct `tierRow`, montagem em `handleStatusPage`, bloco no template) e um teste novo em `main_test.go`. O teste `TestAdminAndStatusUnchangedWithTiers` compara `/status` com e sem tiers; passa a comparar só `/admin/*`, porque a página agora difere de propósito.

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test -race -count=1 ./...` -- expected: todos passam, incluindo o teste novo (tier em cooldown com tempo restante, corpo sem o valor de nenhuma chave nem de tier, sem `PROXY_API_KEY` na request)

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 4 findings — high 0, medium 1, low 3, false 0, maybe-false 0
- findings:
  - `[medium]` `patch` tier 0 aparecia `ok` com todas as chaves esgotadas — `tierRows` agora usa `keys.RetryAfterSeconds` quando não há cooldown de tier
  - `[low]` `patch` README e `docs/operations.md` diziam que `/status` mostra só chaves — frases atualizadas
  - `[low]` `patch` teste não cobria o tier 0 esgotado — asserção adicionada em `TestStatusPageTiersSection`
  - `[low]` `reject` tier extra com o nome literal "OpenCode Go (tier 0)" duplicaria a linha — cosmético, só com nome escolhido de propósito

## Auto Run Result

- Mudança: com tiers extras na config, `/status` mostra abaixo das chaves uma tabela com o tier 0 e cada tier, com estado (`ok` ou `cooldown`) e o tempo restante. Sem tiers extras a página fica igual.
- Arquivos: `adminkeys.go` (`tierRow`, `statusPage`, `tierRows`, bloco no template), `main_test.go` (`TestStatusPageTiersSection`; `TestAdminAndStatusUnchangedWithTiers` passa a comparar só `/admin/*`), `README.md` e `docs/operations.md`.
- Review: 4 achados (1 medium, 3 low); 3 patches aplicados, 1 rejeitado (motivo no log).
- Follow-up recomendado: não.
- Verificação: `go build`, `go vet` e `go test -race -count=1 ./...` passam, antes e depois dos patches.
- Risco residual: a seção aparecer "abaixo das chaves" com o proxy rodando foi checada só por teste, não abrindo no navegador.
