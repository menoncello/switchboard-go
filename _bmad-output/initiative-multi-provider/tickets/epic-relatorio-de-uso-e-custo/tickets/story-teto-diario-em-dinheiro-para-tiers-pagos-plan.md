---
title: 'Teto diário em dinheiro para tiers pagos'
type: 'feature'
ticket: 6
created: '2026-10-30'
status: 'built'
baseline_revision: '761f852a6ed944d19ed872430008239eefd622bc'
route: 'full'
route_source: 'auto'
risk: 'high'
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

**Problem:** Só tiers `free: true` são aceitos; um tier pago não tem como entrar sem risco de gasto sem limite (R5, R10).

**Approach:** Tier sem `free: true` passa a ser aceito se tiver `daily_cap` (USD), `usage_file` e preço para todos os seus modelos. O gasto do dia vem do histórico em `usage_file`; no teto o tier é pulado e o `Retry-After` vai até a virada do dia no fuso. `/status` marca o tier com gasto e teto.

## Boundaries & Constraints

**Always:** o dia é o de `history.loc` (fuso `timezone`), o mesmo do relatório; o gasto do dia de um tier é a soma de `CostUSD` das linhas do dia desse tier; a request em andamento pode passar do teto (o custo só se conhece no fim); nos tiers pagos em stream OpenAI o proxy injeta `stream_options.include_usage = true` (preservando outras chaves de `stream_options`) só no corpo enviado a esse tier, sem vazar para o tier seguinte; resposta 2xx de tier pago sem `usage` soma ao custo o maior preço (`input` ou `output`) de todo o mapa `prices`, em USD, como pior caso, e já conta em "Sem usage"; tier gratuito continua com custo 0 sem usage; `daily_cap` só em YAML.

**Never:** alerta SMTP de primeiro uso (story 7); estimativa pelo tamanho do corpo; teto em requests; teto por chave ou por projeto; mudar o tier 0; alterar os campos do log por request ou do `usage_file`; `daily_cap` em tier `free: true` (rejeitado na validação).

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Config paga válida | `free` ausente, `daily_cap: 5`, `usage_file` e preço para cada modelo | aceita | — |
| Sem teto | tier pago sem `daily_cap` ou com `<= 0`, `NaN`, `Inf` | rejeitada na partida | erro com o tier |
| Sem `usage_file` | tier pago e `usage_file` vazio | rejeitada | erro com o tier |
| Modelo sem preço | modelo do tier (id mapeado) fora de `prices` | rejeitada | erro com tier e id |
| Teto em tier gratuito | `free: true` com `daily_cap` | rejeitada | erro com o tier |
| Teto atingido | gasto do dia do tier `>= daily_cap` | tier pulado, request segue ao próximo tier | — |
| Todos esgotados | tiers elegíveis no teto e tier 0 fora | 429 com `Retry-After` até a virada do dia (mínimo 1), se menor que os outros tempos | — |
| Reinício | app recriado com o mesmo `usage_file` | gasto do dia continua e o tier segue no teto | — |
| Virada do dia | `now` passa da meia-noite do fuso | gasto do tier volta a 0 | — |
| Stream pago sem usage | SSE 2xx sem `usage` | custo += preço máximo do mapa; "Sem usage" +1 | — |
| Stream OpenAI pago | `stream: true`, sem `stream_options` | upstream recebe `include_usage: true`; `stream_options` do cliente preservado | — |
| Stream em tier gratuito | mesmo corpo num tier `free: true` | corpo sem `stream_options` novo | — |
| Anthropic pago | estilo anthropic | corpo inalterado | — |

</intent-contract>

## Code Map

- `config.go` -- `TierConfig` (l.116), `yamlTier` (l.~225, tem `Free`), `convertTiers` (l.231), `validateConfig` (l.494, chama `validatePrices`), `validatePrices` (l.534), `validateTiers` (l.552, l.569 rejeita `!t.Free`: remover), `Config.price` (l.100), `ModelPrice`. `UsageFile`, `Prices` já existem.
- `history.go` -- `usageHistory` (`loc`, `now`, `rows`, `mu`), `record` (l.122; recebe `price *ModelPrice` e `noUsage`), `window` (l.176). Entram `spent(tier string) float64` (soma `CostUSD` das linhas de hoje do tier, sob `mu`) e `untilMidnight() time.Duration` (`time.Date(y, m, d+1, 0,0,0,0, loc)`, seguro em DST).
- `proxy.go` -- `proxyV1` l.130-142 chama `record`; `proxyTiers` l.314-349 itera `a.config.Tiers` e compartilha `fields` entre tiers (copiar o mapa por tier antes de injetar); `localRetryAfter` l.264 só olha cooldowns; `tierModelID` l.364.
- `server.go` -- `App`, `newApp` (l.45); sem mudança prevista.
- `adminkeys.go` -- `tierRow`, `tierRows` (l.361), `statusTmpl` tabela Tiers (l.234-237): marcar tier no teto e mostrar gasto e teto dos pagos.
- `config.go` tem também `mergeConfig` e `loadYAMLConfig`: `daily_cap` vive em `TierConfig`, nada a mesclar.
- `main_test.go` -- testa `validateConfig` e rejeição de tier não gratuito (atualizar); `history_test.go` -- `historyAppWith`, `post`, `tierReq`, `getStatusPage`.
- `docs/configuration.md` (seções de tiers e "Usage history"), `README.md`, YAML de exemplo se houver `free: true` nele.

## Tasks & Acceptance

**Execution:**
- [ ] `config.go` -- `TierConfig.DailyCap float64`, YAML `daily_cap`; `validateTiers` deixa de exigir `free`; nova validação (após `validatePrices`): tier pago exige `daily_cap` finito `> 0`, `UsageFile` e preço para cada `cmp.Or(m.ID, m.Name)`; tier `free` com `daily_cap` é rejeitado; helper `Config.maxPrice() float64` -- R5
- [ ] `history.go` -- `spent(tier)` e `untilMidnight()`; `record` ganha o custo de pior caso para resposta sem usage de tier pago (parâmetro novo) -- R5, R10
- [ ] `proxy.go` -- `proxyTiers` pula tier pago com `spent >= DailyCap`; copia `fields` por tier e injeta `stream_options.include_usage` em tier pago, estilo OpenAI e `stream: true`; `localRetryAfter` inclui `untilMidnight` dos tiers elegíveis no teto; `proxyV1` passa o pior caso quando `usageMissing` e o tier é pago -- R5
- [ ] `adminkeys.go` -- linha do tier pago com gasto e teto (`US$ x / US$ y`), estado "teto" quando atingido, com os segundos até a virada na coluna de cooldown; tier gratuito inalterado -- R5
- [ ] `history_test.go`, `main_test.go` -- matriz inteira: validação (cada rejeição e a aceitação), pulo no teto com a request seguindo ao próximo tier, `Retry-After` até a virada com `now` fixo, gasto sobrevive a recriar o app (`close` e novo `newApp`), virada do dia zera, stream sem usage soma o preço máximo e aparece em "Sem usage", `include_usage` chega ao upstream (com `stream_options` prévio preservado) só no pago e não vaza ao tier seguinte, `/status` mostra gasto e teto; trocar o teste de "free must be true" -- R5, R10
- [ ] `docs/configuration.md`, `README.md`, YAML de exemplo -- `daily_cap`, tier pago exigindo `usage_file` e preços, pior caso sem usage, request em andamento pode passar do teto, `include_usage` injetado; remover "only free tiers" -- R5

**Acceptance Criteria:**
- Given tier pago sem `daily_cap`, sem `usage_file` ou com modelo sem preço, when a config é validada, then é rejeitada.
- Given gasto do dia no teto, when chega uma request, then o tier é pulado e o próximo atende, ou 429 com `Retry-After` até a virada do dia.
- Given gasto gravado em `usage_file`, when o app é recriado, then o tier segue no teto.
- Given `GET /status` com tier pago, when renderiza, then mostra gasto e teto do tier.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-30 — Review pass
- verdicts: 6 findings — high 0, medium 0, low 5, false 1, maybe-false 0
- findings:
  - `[low]` `[patch]` frase de `docs/configuration.md` dizia que resposta sem `usage` custa 0 também em tier pago — corrigida para custo 0 no gratuito e pior caso no pago.
  - `[low]` `[patch]` gasto e teto em `/status` com `%.2f` podiam contradizer o estado `teto` — passou a `%.4f`, teste ajustado.
  - `[low]` `[reject]` stream pago cancelado pelo cliente paga o pior caso — é o que o intent manda para resposta sem `usage`; o conserto edita o intent.
  - `[low]` `[patch]` faltava teste de `localRetryAfter` com cooldown menor que a virada do dia — `TestPaidTierCapRetryAfterShorterCooldownWins`.
  - `[low]` `[patch]` faltava teste de tier pago lido do YAML — `TestLoadConfigPaidTier`.
  - `[false]` `[reject]` ausência de `AGENTS.md`/`CLAUDE.md` no repositório — não é defeito do código.
- Comentário errado de `capApp` (unidade do preço) foi corrigido junto, como parte do achado dos testes.

## Auto Run Result

**Resumo:** tier sem `free: true` é aceito com `daily_cap` (USD), `usage_file` e preço para todos os modelos. O gasto do dia vem do histórico; no teto o tier é pulado e o `Retry-After` vai até a virada do dia no fuso. Tier pago em stream OpenAI recebe `include_usage`; resposta sem `usage` soma o maior preço do mapa e entra em "Sem usage". `/status` mostra gasto e teto do tier.

**Arquivos:**
- `config.go` -- `DailyCap`, `validatePaidTiers`, `maxPrice`, `paidTier`
- `history.go` -- `spent`, `untilMidnight`, pior caso em `record`
- `proxy.go` -- pulo no teto, `include_usage` por tier, `Retry-After`
- `adminkeys.go` -- coluna de gasto e teto
- `history_test.go`, `main_test.go` -- testes da matriz
- `docs/configuration.md`, `README.md` -- `daily_cap` e tiers pagos

**Revisão:** 6 achados, 4 patches aplicados (doc, formato do gasto, 2 testes), 0 adiados, 2 rejeitados: stream cancelado paga o pior caso (decisão do intent) e falta de `AGENTS.md`/`CLAUDE.md` (não é código).
**Follow-up recomendado:** false.
**Verificação:** `go vet ./...` e `go test -race -count=1 ./...` verdes. Não rodei o proxy contra provedor real nem abri `/status` no navegador.
**Risco residual:** `usage_file` ilegível zera o gasto do dia e relaxa o teto; stream cancelado antes do `usage` conta o pior caso e pode travar o tier até a virada do dia; `spent` varre todas as linhas por request.

## Design Notes

"Preço máximo do mapa" é lido como o maior `input` ou `output` de todo `prices`, somado como USD por request: pior caso deliberado (equivale a 1M tokens), que trava o teto cedo em vez de subestimar. Tier gratuito mantém custo 0 sem usage (3.5).

## Verification

**Commands:**
- `go vet ./...` -- expected: sem aviso
- `go test -race -count=1 ./...` -- expected: verde, com os testes novos
