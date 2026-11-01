---
title: 'Erro de transporte passa por writeAPIError sem a URL do upstream'
type: 'bugfix'
ticket: '1'
created: '2026-11-01'
status: done
baseline_revision: '29af5a845b4e194c5bbbce6b2a02f5b90e86ccb5'
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

**Problem:** Em erro de transporte, `serveProxy` responde com `http.Error("upstream unreachable")`, texto puro fora do formato de erro da API, e o detalhe (com a URL do upstream) não vai a lugar nenhum.

**Approach:** Responder por `writeAPIError` com mensagem genérica e logar o erro real em `a.logger` onde ele é capturado (proxy.go, tier 0 e tiers extras).

</intent-contract>

## Implementation Notes

Oneshot: mudança de ~10 linhas em proxy.go e um teste ajustado. O teste existente `TestFailoverTier0UnreachableWithoutExtraTiersHidesTierURL` passa a exigir o formato JSON da API e o log com o detalhe.

## Verification

**Commands:**
- `go test ./...` -- expected: ok

## Review Triage Log

### 2026-11-01 — Review pass
- verdicts: 5 findings — high 0, medium 0, low 5, false 0, maybe-false 0
- findings:
  - `[low]` `[patch]` Teste não cobria o formato Anthropic nem o Content-Type — segundo request com header `anthropic-version` e asserção do corpo adicionada
  - `[low]` `[patch]` Log do tier extra sem asserção — teste de todos inalcançáveis agora exige a linha `tier upstream unreachable` com `tier=t1`
  - `[low]` `[reject]` Query string do cliente vai ao log junto da URL — o ticket pede a URL no log; o log é do operador e a query é do próprio cliente
  - `[low]` `[reject]` Warn de tier 0 inalcançável mesmo quando um tier extra atende — diagnóstico útil, sem dano
  - `[low]` `[patch]` Teste de todos inalcançáveis afrouxado para `l[len(l)-1]` — resolvido pela asserção das linhas de Warn (`len(l) == 3`)

## Auto Run Result

- **Mudança:** erro de transporte responde por `writeAPIError` (502, `upstream_unreachable`, mensagem genérica); o erro real vai ao log do tier 0 e dos tiers extras. Cancelamento do cliente não loga.
- **Arquivos:** `proxy.go` (resposta e logs), `main_test.go` (teste do tier 0 com JSON, Anthropic e log; teste de todos inalcançáveis com as linhas de Warn).
- **Review:** 3 patches aplicados, 2 rejeitados com motivo no log, nada adiado.
- **Revisão de acompanhamento:** não recomendada.
- **Verificação:** `go test ./...` ok, `go vet` e `gofmt` limpos.
- **Riscos residuais:** a query string do cliente aparece no log dentro da URL.

## Code Review

### 2026-11-01
0 `decision-needed`, 2 `patch`, 0 `defer`, 17 rejeitados.

- [ ] [Review][Patch] (não aplicado: tier 0 não tem nome, o log de request usa tier "") Warn do tier 0 sem atributo `tier`, diferente do Warn dos tiers extras [proxy.go:193]
- [x] [Review][Patch] Asserções fracas nos testes: `l[0]` (Warn do tier 0) não checado, mensagem genérica não asserida, `error.type` do estilo Anthropic não checado [main_test.go:2349-2367, main_test.go:2404-2409]

**Rejected**
- Query do cliente no log junto da URL (3 achados): log do operador, a query é do próprio cliente, o plano já aceitou o risco e a correção exige helper de redação.
- Timeout vira 502 em vez de 504: 502 era o status anterior e a intenção pede 502 genérico.
- Warn sem limite de taxa: só incomoda em queda prolongada e a correção traz estado.
- `writeOpenAIError` fixa `invalid_request_error`: comportamento antigo de todo o helper, fora deste diff.
- Tipo Anthropic inválido: falso, `anthropicErrorType` devolve `api_error` para 5xx.
- Cancelamento sem teste: falso, `TestFailoverClientCancelStopsWithoutTouchingState` cobre.
- Comentário obsoleto: falso, continua verdadeiro.
- `ticket: '1'` no plano: falso, é o id local no epic. Demais itens de doc sem dano.
- Verification-gap sem lacunas; Intent Alignment descritivo, sem defeito.
