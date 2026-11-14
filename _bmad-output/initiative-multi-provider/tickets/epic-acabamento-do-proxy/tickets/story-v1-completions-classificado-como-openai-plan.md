---
title: '/v1/completions classificado como OpenAI'
type: 'bugfix'
ticket: '3'
created: '2026-11-01'
status: done
baseline_revision: '6b1a0628201de12f76d65ac23ae945e2ec4c4b67'
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

**Problem:** `apiStyleForRequest` testa `HasPrefix(path, "/complete")`, que também casa `/v1/completions`; o legado de completions da OpenAI é classificado como Anthropic, sai com `x-api-key` e pula tiers só OpenAI.

**Approach:** Trocar o prefixo por igualdade com `/complete`, o caminho legado da Anthropic, de modo que `/v1/completions` caia no estilo OpenAI.

</intent-contract>

## Implementation Notes

Oneshot: uma linha em `proxy.go:466` e um teste de `apiStyleForRequest` em `main_test.go` (hoje sem teste direto): `/v1/completions` é OpenAI, `/v1/complete` e `/v1/messages` são Anthropic, e um tier só OpenAI atende `/v1/completions`.

## Plan Change Log

## Review Triage Log

### 2026-11-01 — Review pass
- verdicts: 3 findings — high 0, medium 0, low 2, false 0, maybe-false 0 (+1 sem defeito)
- findings:
  - `[low]` `[patch]` docs/configuration.md ainda dizia que o caminho "começa com /v1/complete" — texto trocado para "exatamente /v1/complete"
  - `[low]` `[reject]` Corrida de dados em hits/auth/apiKey no teste do tier — a leitura vem depois da resposta HTTP, que sincroniza; o CI não roda -race; corrigir pede mutex/atomics
  - `[low]` `[reject]` /messages segue por prefixo — intencional, /v1/messages/batches e count_tokens são Anthropic

## Verification

**Commands:**
- `go test ./...` -- expected: ok
- `go vet ./... && gofmt -l .` -- expected: sem saída

## Auto Run Result

- **Mudança:** `apiStyleForRequest` compara o caminho com `/complete` por igualdade em vez de prefixo. Premissa do ticket não se confirmou: `/v1/completions` já saía como OpenAI, pois `"/completions"` não começa com `"/complete"`. Os testes novos passam também com o código antigo e valem como regressão.
- **Arquivos:** `proxy.go` (uma linha), `main_test.go` (`TestAPIStyleForRequest`, `TestV1CompletionsServedByOpenAIOnlyTier`), `docs/configuration.md`.
- **Review:** 1 patch (doc), 2 rejeitados com motivo no log.
- **Revisão de acompanhamento:** não recomendada.
- **Verificação:** `go test ./...` ok, `go vet` e `gofmt` limpos.
- **Riscos residuais:** `/v1/complete/` com barra final passa a cair em OpenAI.

## Code Review

### 2026-11-01
0 decision-needed, 2 patch, 0 defer, 14 rejected.

- [x] [Review][Patch] Nenhum teste separa `==` de `HasPrefix`: adicionar `/v1/complete/` e `/v1/completeX` (esperado OpenAI) em `TestAPIStyleForRequest` [main_test.go:3480-3500]
- [x] [Review][Patch] `TestV1CompletionsServedByOpenAIOnlyTier` lê `hits`/`auth`/`apiKey` sem sincronização; usar atomic/mutex como os testes vizinhos [main_test.go:3511-3523]

Rejected:
- `/v1/complete/` e subcaminhos viram OpenAI (R1, R2): false, é o comportamento pedido (igualdade).
- Premissa do ticket falsa / testes passam com o código antigo (B1, I1): corrigir editaria a intenção do plano.
- Metadado `ticket: '3'` vs `5.3` (B7): false, ref 5.3 é o id 3 do epic.
- `s0` sem asserção (R5, B4), doc sem `/v1` opcional (R6, B6), faltam linhas batches/sem `/v1` (B3, V3): low, pouco visível.
