---
title: 'Tabela de preços e custo no relatório'
type: 'feature'
ticket: 5
created: '2026-10-29'
status: done
baseline_revision: '1b57f89460e474e4f92a27c1f5a455dcfd3d4bef'
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

**Problem:** O relatório (3.4) só mostra tokens: o Eduardo não vê quanto cada projeto, modelo e tier custou (R13).

**Approach:** `prices:` na config (USD por 1M tokens de entrada e de saída, por tier e id do modelo mapeado), validada por `validateConfig`. O custo de cada request é somado no histórico, persistido em `usage_file`, e aparece no relatório onde há preço; onde não há, só tokens. O tier 0 não tem preço.

## Boundaries & Constraints

**Always:** preço em USD por 1M tokens, chave `prices.<tier>.<id do modelo mapeado>` com `input` e `output`; o custo da request é `(in*input + out*output)/1e6` sobre os tokens que a resposta reportou; tokens de cache não têm preço próprio (contam em `in` como já vêm); o custo entra na linha do histórico no momento da request, então mudar `prices` depois não reescreve o passado; `usage_file` antigo, sem os campos novos, carrega com custo zero e sem preço; `prices` só em YAML.

**Never:** teto diário, `daily_cap`, tier pago, alerta SMTP, exigir preço para todos os modelos (3.6 em diante); estimativa de custo para resposta sem `usage` (a request conta como com preço, custo 0, e aparece em "Sem usage"); preço para o tier 0; endpoint JSON novo; mudar os campos já gravados do `usage_file` ou os campos do log por request.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Modelo com preço | tier com `m` (id `m-id`), preço input 1.0 e output 2.0, resposta com 1000 in e 500 out | custo 0.002 USD na linha do histórico e no relatório (`US$ 0.0020`) | — |
| Modelo sem preço | tier sem entrada em `prices` para o id | relatório mostra `—` no custo, só tokens | — |
| Tier 0 | request servida pelo tier 0 | sem custo, `—` | — |
| Preço negativo | `input: -1` ou `output: -1` | `validateConfig` rejeita | erro com tier e id |
| Tier desconhecido | `prices` com tier que não está em `tiers` (inclui `opencode-go`) | `validateConfig` rejeita | erro com o nome |
| Persistência | app recriado a partir de `usage_file` | custo e contagem de requests com preço continuam | — |
| Linha mista | dimensão com requests com e sem preço | `US$ x (n/m com preço)` | — |
| Sem usage | resposta 2xx sem `usage` em tier com preço | custo 0, conta como com preço e em "Sem usage" | — |

</intent-contract>

## Code Map

- `config.go` -- `Config` (l.20-64), `TierConfig`, `TierModel` (`Name` do cliente, `ID` mapeado), `yamlConfig` (l.135), `loadYAMLConfig` (l.232, o literal de retorno l.276), `mergeConfig` (l.279), `validateConfig` (l.436) e `validateTiers` (l.473). `prices` entra no mesmo padrão de `usage_file`/`timezone`.
- `proxy.go` -- `proxyV1` l.130-138 chama `a.history.record(project, model, key, tier, usage, usageMissing)`; `tierModelID` (l.355) mostra como `TierModel.Name` vira `ID` (`ID` vazio = `Name`); `tier0Name` (l.18) é o tier da request do tier 0, que não está em `config.Tiers`.
- `history.go` -- `usageCounts` (l.27), `record` (l.117), `load` (l.74, soma campo a campo), `usageRow`. Os campos novos entram em `usageCounts` e são somados em `load` e `record`.
- `adminkeys.go` -- `usageLine`, `usageReport()` (l.280), `statusTmpl` (l.211-236): coluna de custo em cada tabela e no total do dia.
- `history_test.go` -- `historyAppWith`, `post`, `tierReq` (main_test.go), `getStatusPage`, `TestStatusUsageSection`; `main_test.go` tem testes de `validateConfig` e de config YAML para copiar o padrão.
- `docs/configuration.md` l.88-93 (exemplo) e l.122-157 ("Usage history"), `README.md` l.154-157.

## Tasks & Acceptance

**Execution:**
- [ ] `config.go` -- `ModelPrice{Input, Output float64}`; `Config.Prices map[string]map[string]ModelPrice`; campo YAML `prices` (tier → id → `{input, output}`), repassado em `loadYAMLConfig`, copiado em `mergeConfig` quando não vazio; `validateConfig` rejeita preço negativo e tier fora de `Tiers` (o tier 0 cai aqui) -- sem env var
- [ ] `config.go` -- `func (c Config) price(tier, model string) (ModelPrice, bool)`: acha o tier em `Tiers`, mapeia `model` (nome do cliente) para o id como em `tierModelID` e busca em `Prices`
- [ ] `history.go`, `proxy.go` -- `usageCounts` ganha `CostUSD float64 \`json:"cost_usd"\`` e `Priced int \`json:"priced"\``; `record` recebe o preço (ou nada) e soma custo (só quando `usage.found`) e `Priced` (sempre que há preço); `load` soma os dois; `proxyV1` consulta `a.config.price(tier, model)` e passa a `record`
- [ ] `adminkeys.go` -- `usageLine` soma `CostUSD` e `Priced`; coluna "Custo" em cada tabela e no total do dia: `—` sem `Priced`, `US$ %.4f` com `Priced == Requests`, `US$ %.4f (n/m com preço)` nos demais
- [ ] `history_test.go`, `main_test.go` -- testes da matriz: custo certo de um modelo com preço (com `id` diferente do nome), sem preço e tier 0 em `—`, `validateConfig` com preço negativo e tier desconhecido, `prices` lido do YAML, custo mantido ao recriar o app com o mesmo `usage_file`, `usage_file` antigo carrega, linha mista, sem usage
- [ ] `docs/configuration.md`, `README.md` -- `prices:` no exemplo e em "Usage history": USD por 1M tokens, chave tier e id mapeado, cache sem preço próprio, custo fixado na hora da request, tier 0 sem preço, `cost_usd` e `priced` no JSON do arquivo; trocar "Only tokens and requests: no price" e a frase do README

**Acceptance Criteria:**
- Given um tier com preço para o id mapeado e uma request com `usage`, when `GET /status`, then o total do dia e as tabelas mostram o custo em USD certo.
- Given um modelo sem preço, when `GET /status`, then o custo é `—` e os tokens aparecem.
- Given `prices` com valor negativo ou tier desconhecido, when a config é validada, then `validateConfig` retorna erro.
- Given um `usage_file` com custo gravado, when o app é recriado, then o custo continua no relatório.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 5 findings — high 0, medium 1, low 4, false 0, maybe-false 0
- findings:
  - `[medium]` `[patch]` preço `+Inf` (`.inf` no YAML) passa em `validatePrices`, o custo vira Inf/NaN e `json.Marshal` falha em `flush`, então o `usage_file` não é gravado — `validatePrices` rejeita não finitos, caso `inf` em `TestValidateConfigPrices`.
  - `[low]` `[reject]` chave faltando ou digitada errada em `prices` vira preço 0 — o conserto pede ponteiros ou `KnownFields` no decode da config inteira; entrada de config feita à mão e o custo 0 aparece como `US$ 0.0000`.
  - `[low]` `[reject]` `validatePrices` e `price()` comparam o nome do tier sem `TrimSpace` — o histórico grava o nome cru, então tudo é consistente; só um nome com espaço sobrando diverge.
  - `[low]` `[patch]` frase de `docs/configuration.md` ainda dizia total só de requests e tokens — atualizada para incluir custo.
  - `[low]` `[patch]` sem teste do ramo `mergeConfig` de `Prices` e do tier 0 em `validateConfig` — `TestMergeConfigPrices` criado; o caso do tier 0 já existia em `TestValidateConfigPrices`.

## Auto Run Result

**Resumo:** `prices:` na config (USD por 1M tokens, por tier e id mapeado) validada por `validateConfig`. O custo de cada request é gravado na linha do histórico (`cost_usd`, `priced`) e aparece na coluna "Custo" de cada tabela e no total do dia de `/status`; sem preço, `—`. O tier 0 não tem preço.

**Arquivos:**
- `config.go` -- `ModelPrice`, `Config.Prices`, `price()`, `validatePrices`
- `history.go`, `proxy.go` -- `CostUSD` e `Priced` em `usageCounts`, `record` com preço
- `adminkeys.go` -- coluna e total de custo
- `history_test.go` -- testes da matriz, validação, YAML e merge
- `docs/configuration.md`, `README.md` -- `prices:` e custo

**Revisão:** 5 achados (1 medium, 4 low), 3 patches aplicados (preço não finito, teste de merge, frase de doc), 0 adiados, 2 rejeitados: chave faltando em `prices` vira 0 (conserto pede mudar o decode da config) e nome de tier sem `TrimSpace` (consistente com o histórico).
**Follow-up recomendado:** false.
**Verificação:** `go vet ./...` e `go test -race -count=1 ./...` verdes. Não abri `/status` no navegador.
**Risco residual:** `prices` sem `input` ou `output` vira 0 e o relatório mostra `US$ 0.0000`; cache de tokens não tem preço próprio; resposta sem `usage` conta custo 0.

## Design Notes

O custo é gravado na linha em vez de recalculado a partir de tokens: o preço é digitado à mão e muda; o histórico não pode mudar junto. `Priced` conta requests com preço para o relatório distinguir "sem preço" de "custo zero" e mostrar custo parcial numa dimensão mista.

## Verification

**Commands:**
- `go vet ./...` -- expected: sem aviso
- `go test -race ./...` -- expected: verde, com os testes novos

## Code Review

### 2026-10-30
0 `decision-needed`, 5 `patch`, 0 `defer`, 16 rejected.

- [x] [Review][Patch] `input` ou `output` faltando em `prices` vira 0 e subestima o custo; `ModelPrice` com `*float64` e `validatePrices` exigindo as duas chaves (decidido: opção 1) [config.go:212]
- [x] [Review][Patch] `validatePrices` aceita id de modelo que o tier não mapeia (typo mostra `—` para sempre) [config.go:285]
- [x] [Review][Patch] Sem teste da soma de custo e `Priced` entre várias linhas em `usageReport`/`record` (2 requests com preço + 1 sem; segundo post em `TestCostPricedModel`) [history_test.go]
- [x] [Review][Patch] Sem teste de `Config.price` com `ID` vazio, `ID` explícito, modelo e tier desconhecidos [history_test.go]
- [x] [Review][Patch] `%.4f` mostra `US$ 0.0000` para custo pequeno e não nulo; usar mais casas abaixo de 0.0001 [adminkeys.go:168]

#### Rejected
- Sem usage conta como com preço, `US$ 0.0000` (blind): contrato do intent manda assim e a doc diz.
- Cache de tokens sem preço, Anthropic subestima (edge): contrato do intent; o conserto edita o intent.
- `price()` copia `Config` (blind): cópia desprezível.
- Ordem de erro de `validatePrices` não fixa (blind): raro, ninguém depende.
- Plano com tarefas sem marcar e Code Map desatualizado (blind): artefato do fluxo, sem efeito no código.
- `/status` não aberto no navegador (blind): não é defeito de código.
- `docs/admin-api.md` sem atualização (blind): a página não descreve a seção de uso. Linhas longas em README/docs: cosmético.
- Modelo truncado em 128 bytes antes de `price()` (edge): nome de modelo de tier com mais de 128 bytes não é realista.
- Preço enorme estoura para Inf e quebra `flush` (edge, verification-gap): exige preço acima de 1e302.
- `Priced > Requests` e `cost_usd` negativo em `usage_file` (edge): só arquivo editado à mão.
- Teste do tier 0 com `prices` e validação pelo caminho YAML (intent): cobertos pelos testes existentes e pelo `validateConfig` no startup.
