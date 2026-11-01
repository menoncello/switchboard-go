---
title: '404 de modelo inexistente segue ao próximo tier'
type: 'bugfix'
ticket: '2'
created: '2026-11-01'
status: done
baseline_revision: 'bfb3ba28ed3ff1f4e1389fdfe625d8b1fd7b8d3d'
route: 'oneshot'
route_source: 'auto'
risk: 'medium'
review: 'quick'
review_source: 'pinned'
lenses_ran: [quick]
review_loop_iteration: 0
followup_review_recommended: false
context: []
warnings: []
deferred:
  - summary: >-
      Um 404 de modelo inexistente que nenhum tier atende dispara o alerta Telegram "no tier could serve a request".
    evidence: |-
      Antes o 404 ia direto ao cliente sem passar por notifyFailure; agora é retido e, sem tier que atenda, proxyV1 chama notifyFailure. Um typo de modelo do cliente gera falso alerta de queda. Comportamento decorre da intenção do ticket; filtrar exige decidir se erro de cliente alerta.
    location: >-
      proxy.go:227
    severity: low
---

<intent-contract>

## Intent

**Problem:** `isTierFailure` devolve false para um 404 de modelo inexistente, então a request para no tier e o cliente recebe o 404 mesmo havendo próximo tier mapeado.

**Approach:** `isTierFailure` passa a devolver true para todo 404 com modelo na request; um helper `isModelNotFound` marca o 404 de modelo inexistente, e os dois pontos que põem o tier em cooldown (tier 0 e tiers extras em `proxy.go`) pulam o cooldown nesse caso. 400 e 403 seguem direto ao cliente. `TestFailover400And404MissingModelPassThroughWithoutCooldown` é ajustado.

</intent-contract>

## Implementation Notes

Oneshot: ~20 linhas em `proxy.go` (`isTierFailure` linha 324, chamadas nas linhas 209 e 378) e o teste em `main_test.go:2148`. O 404 de modelo inexistente do último tier continua chegando ao cliente, pelo `heldResponse`.

## Plan Change Log

## Review Triage Log

### 2026-11-01 — Review pass
- verdicts: 3 findings — high 0, medium 0, low 3, false 0, maybe-false 0
- findings:
  - `[low]` `[patch]` docs/configuration.md descrevia o 404 de modelo como resposta ao cliente — texto da seção Tiers reescrito
  - `[low]` `[defer]` 404 de modelo sem tier que atenda dispara o alerta Telegram de falha — consequência do comportamento pedido; decidir se erro de cliente alerta fica para depois
  - `[low]` `[reject]` Sem teste de que um 404 que não é de modelo ainda põe cooldown — os testes existentes de 404 "route gone" (tier 0 e extra) já exigem o tier 1/2 atendendo na 2ª request sem nova chamada ao tier falho

## Verification

**Commands:**
- `go test ./...` -- expected: ok
- `go vet ./... && gofmt -l .` -- expected: sem saída

## Auto Run Result

- **Mudança:** `isTierFailure` passa a tratar todo 404 com modelo na request como falha do tier; o novo `isModelNotFound` faz o tier 0 e os tiers extras pularem o cooldown no 404 de modelo inexistente. O 400 e o 403 seguem ao cliente.
- **Arquivos:** `proxy.go` (classificação e dois pontos de cooldown), `main_test.go` (teste antigo dividido em 400/403 e 404 de modelo, com tier 0, tier extra e último tier), `docs/configuration.md` (seção Tiers).
- **Review:** 1 patch aplicado (doc), 1 adiado (alerta Telegram em 404 de modelo sem tier), 1 rejeitado com motivo no log.
- **Revisão de acompanhamento:** não recomendada.
- **Verificação:** `go test ./...` ok, `go vet` e `gofmt` limpos.
- **Riscos residuais:** typo de modelo do cliente pode disparar o alerta Telegram.

## Code Review

### 2026-11-01 — 0 decision-needed, 4 patch, 0 defer, 13 rejected

- [x] [Review][Patch] Apertar `isModelNotFound`: exigir código/tipo `model_not_found` ou "model" junto da frase, e testar um 404 de rota morta com "model" no corpo [proxy.go:335-349]
- [x] [Review][Patch] Alerta Telegram `notifyFailure` dispara quando o 404 de modelo não é atendido por nenhum tier [proxy.go:230]
- [x] [Review][Patch] Sem teste de 404 de modelo retido como última resposta com corpo > 4096 bytes, conferido byte a byte [main_test.go:2189]
- [x] [Review][Patch] Comentário de `isTierFailure` diz "Like isKeyDead, it restores resp.Body", mas a função não lê mais o corpo [proxy.go:~323]

#### Rejected
- Corpo 404 retido sobrescrito por falha de tier posterior (blind, edge): low, corrigir exige lógica de prioridade.
- Tier reacionado a cada request após 404 de modelo (edge): é o que o ticket pede (sem cooldown).
- Testes ausentes (sem model, tier único, unit): 404 sem model já coberto por TestFailover404WithoutRequestModelPassesThrough; resto low.
- Docs sem efeitos colaterais: low, pouco visível.
- Metadados do plano (`ticket: '2'`, risk/review): falso, id dentro do epic e campo de processo.
- Intent: predicado vs chamadores, teste renomeado: low, descritivo. No-cooldown no tier extra só por hits: falso, `hits == i` prova.
