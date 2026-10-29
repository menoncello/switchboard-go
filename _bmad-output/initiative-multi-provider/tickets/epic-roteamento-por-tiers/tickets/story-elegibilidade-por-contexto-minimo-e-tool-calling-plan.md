---
title: 'Elegibilidade por contexto mínimo e tool calling'
type: 'feature'
ticket: 6
created: '2026-10-29'
status: 'built'
baseline_revision: 'a7364d61a316fdc124260f1ee3d91111aacb4a95'
route: 'full'
route_source: 'auto'
risk: 'medium'
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

**Problem:** Cada modelo do mapa de um tier já declara `context` e `tools`, mas nada os usa: `tierModelID` só olha estilo e nome, então um modelo com contexto pequeno (ou sem declarado) ou sem tool calling atende qualquer request.

**Approach:** `min_context` global na config (padrão 64000, o mínimo do Hermes). Um modelo mapeado só é elegível no tier se `context >= min_context` (contexto 0/ausente = não elegível) e, quando a request traz `tools`, se `tools: true`. O tier 0 é isento. Tier pulado por inelegibilidade segue o mesmo caminho do pulo por estilo/mapeamento.

## Boundaries & Constraints

**Always:** `min_context` vem só do YAML (top-level, ao lado de `limits:`); omitido = 64000; `0` é válido e desliga o piso; negativo é rejeitado em `validateConfig`. A checagem de `tools` só vale quando o body tem `tools` não vazio. Os mesmos critérios valem em `localRetryAfter`, para o `Retry-After` não contar um tier inelegível.

**Never:** Filtrar o tier 0. Traduzir ou degradar a request (tirar `tools` do body). Variável de ambiente para `min_context`. Mexer em estilos, cooldown ou formato do mapa de modelos.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Contexto abaixo do mínimo | tier 1 mapeia o modelo com `context` < `min_context`; tier 0 em cooldown | tier 1 pulado, sem chamada ao upstream dele; cliente recebe o 429 local | — |
| Contexto não declarado | modelo mapeado sem `context` (0) e `min_context` > 0 | tier pulado | — |
| Atinge o mínimo | `context` == `min_context` | tier atende | — |
| Request com tools, modelo sem tools | body com `tools` não vazio, `tools: false` | tier pulado | — |
| Request sem tools, modelo sem tools | body sem `tools` (ou `[]`/`null`) | tier atende | — |
| `min_context: 0` | qualquer `context` | contexto não filtra; `tools` ainda vale | — |
| `min_context` negativo | YAML `min_context: -1` | partida rejeitada | `min_context must be >= 0` |

</intent-contract>

## Code Map

- `config.go` -- `Config` (campo novo `MinContext int`), `defaultConfig` (64000), `yamlConfig` (`min_context *int`; `loadYAMLConfig` passa -1 quando ausente, como `retry`), `mergeConfig` (`>= 0` sobrescreve), `validateConfig` (< 0 rejeita). `TierModel.Context/Tools` já existem e já são parseados.
- `proxy.go` -- `tierModelID(t, model, style)` é o ponto único do filtro, chamado por `proxyTiers` e `localRetryAfter`; `proxyV1` já tem `fields` (via `topLevelModel`) de onde sai o `tools`. Reutilizar `tierModelID`, não criar segundo filtro.
- `main_test.go` -- fixture `tierOK` (~l.1343) e testes de tiers: modelos com `context` ausente passam a ficar inelegíveis; ajustar os fixtures dos testes de failover (`context` >= padrão ou `min_context` explícito) e `tools: true` onde a request leva tools.
- `docs/configuration.md` -- exemplo YAML (l.~77-87) e tabela de campos do tier (l.~145); seção "Tiers".

## Tasks & Acceptance

**Execution:**
- [ ] `config.go` -- `MinContext` na `Config` (padrão 64000), YAML `min_context`, merge, validação `>= 0`
- [ ] `proxy.go` -- `tierModelID` passa a receber `minContext int` e `needTools bool` e retorna `false` se `m.Context < minContext` ou (`needTools && !m.Tools`); `proxyV1` calcula `needTools` uma vez a partir de `fields["tools"]` (presente, não `null`, não `[]`) e o repassa a `proxyTiers` e `localRetryAfter`
- [ ] `main_test.go` -- ajustar fixtures existentes e cobrir cada linha da matriz (inclui `min_context` ausente = 64000, `0` e negativo; `localRetryAfter` ignora tier inelegível)
- [ ] `docs/configuration.md` -- `min_context` no exemplo e numa subseção "Eligibility" (context, tools, tier 0 isento, contexto ausente = inelegível, request sem tools); `context` e `tools` descritos na tabela de `models`

**Acceptance Criteria:**
- Given um modelo mapeado com contexto abaixo do mínimo, when a request chega com o tier 0 em cooldown, then o tier é pulado e o cliente recebe o 429 local.
- Given uma request com `tools` e um modelo `tools: false`, when o tier 0 falha, then o tier é pulado; given a mesma request sem `tools`, then o modelo atende.
- Given um modelo com `context` igual a `min_context`, when o tier 0 falha, then o tier atende.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 3 findings — high 0, medium 0, low 3, false 0, maybe-false 0
- findings:
  - `[low]` `reject` fixtures de failover existentes não ajustados (rodam com `MinContext` 0) — o piso de 64000 pelo caminho YAML está em `TestLoadConfigMinContext` e a elegibilidade ponta a ponta nos testes novos; ajustar os fixtures só soma churn
  - `[low]` `patch` `tools: [ ]` com espaço contava como request com tools — `needTools` agora decodifica o array e testa `len > 0`
  - `[low]` `patch` comentários de `localRetryAfter` e `proxyTiers` falavam só de modelo e estilo — atualizados

## Auto Run Result

- Mudança: `min_context` global (padrão 64000, 0 desliga, negativo rejeitado); `tierModelID` exige `context >= min_context` e, para request com `tools` não vazio, `tools: true`; tier 0 isento; `localRetryAfter` usa o mesmo critério.
- Arquivos: `config.go` (campo, YAML, merge, validação), `proxy.go` (filtro e `needTools`), `main_test.go` (3 testes novos cobrindo a matriz), `docs/configuration.md` (exemplo, subseção Eligibility, tabela de models).
- Review: 3 achados low; 2 patches aplicados, 1 rejeitado (motivo no log).
- Follow-up recomendado: não.
- Verificação: `go build`, `go vet`, `go test -race -count=1 ./...` passam, antes e depois dos patches.
- Risco residual: nenhum conhecido.

## Design Notes

O tier 0 não passa por `tierModelID`, então já é isento sem código novo. `tools` vazio (`[]`) conta como sem tools: nem toda request que traz a chave a usa.

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test -race -count=1 ./...` -- expected: todos passam, incluindo os novos
