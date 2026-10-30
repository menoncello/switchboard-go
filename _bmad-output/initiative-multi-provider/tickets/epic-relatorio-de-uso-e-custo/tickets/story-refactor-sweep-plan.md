---
title: 'Refactor sweep'
type: 'chore'
ticket: 8
created: '2026-10-30'
status: 'done'
baseline_revision: '540398f3046d5422ca0557b6046c36fc259ea8c7'
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

**Problem:** O épico adiou um único achado (3.6, `include_usage` em qualquer caminho OpenAI); os planos 3.1 a 3.7 não deixam notas de implementação nem `TODO`/`FIXME` no código. A única sobra de limpeza são as referências de linha de `deferred-work.md`, que ficaram defasadas.

**Approach:** Só cleanup, sem comportamento novo: atualizar em `deferred-work.md` as referências de linha defasadas dos itens do épico e anotar nele que o `include_usage` segue adiado (mudar o caminho aceito é comportamento, vira story). O item de 404 segue aberto (404 que cita o modelo não passa ao próximo tier) e só tem a referência atualizada.

</intent-contract>

## Implementation Notes

Oneshot: edição de uma linha por item em `_bmad-output/initiative-multi-provider/deferred-work.md`; nenhum arquivo Go muda. Sobras varridas: `include_usage` (`proxy.go:355-358` → `360-363`), `apiStyleForRequest` (`proxy.go:352` → `458`), 404 (`proxy.go:213-223` → `isTierFailure`, `320-340`, segue aberto).

## Plan Change Log

## Review Triage Log

### 2026-10-30 — Review pass
- verdicts: 6 findings — high 0, medium 1, low 5, false 0, maybe-false 0
- findings:
  - `[medium]` `[patch]` nota dizia que o 404 estava resolvido — `isTierFailure` ainda não segue em 404 que cita o modelo; nota reescrita como "segue aberto".
  - `[low]` `[patch]` intervalo do 404 impreciso — passou a apontar `isTierFailure`, `proxy.go:320-340`.
  - `[low]` `[patch]` intervalo do `include_usage` errado por uma linha — corrigido para `360-363`.
  - `[low]` `[patch]` o item `apiStyleForRequest` ainda citava `proxy.go:352` — corrigido para `458`.
  - `[low]` `[patch]` item do sweep (linha 15) dizia "atualizar na story que tratar" — reescrito como atualizado na 3.8.
  - `[low]` `[reject]` faltava nota de que o `include_usage` segue adiado — o item já está em `deferred-work.md` como aberto.

## Verification

**Commands:**
- `go build ./... && go test -race -count=1 ./...` -- expected: verde, sem diff em `*.go`

## Auto Run Result

**Resumo:** varredura do épico: o único achado adiado (`include_usage`, 3.6) é mudança de comportamento e fica em `deferred-work.md`; os planos 3.1 a 3.7 não têm notas de implementação nem há `TODO`/`FIXME` no código. Só as referências de linha de `deferred-work.md` foram atualizadas.

**Arquivos:** `deferred-work.md` -- referências de linha atualizadas; o item de 404 ganhou nota de que segue aberto.

**Revisão:** 6 achados, 5 patches aplicados (a nota errada de "resolvido" no 404 e quatro referências de linha), 0 adiados, 1 rejeitado (nota redundante sobre `include_usage`).
**Follow-up recomendado:** false.
**Verificação:** `go build ./...` e `go test -race -count=1 ./...` verdes; nenhum `*.go` mudou.
**Risco residual:** o `include_usage` em `/v1/responses` segue sem verificação e o 404 de modelo inexistente segue sem passar ao próximo tier; ambos precisam de story própria.

## Code Review

### 2026-10-30
1 `decision-needed`, 1 `patch`, 0 `defer`, 8 rejected.

- [x] [Review][Patch] (decidido: símbolos no lugar de linhas) Referências por número de linha em `deferred-work.md` vão defasar de novo — hoje os números estão corretos (`proxy.go:320-340`, `:458`, `:360-363`), mas a próxima edição de `proxy.go` os quebra, o defeito que esta story corrige. Options: (a) trocar por símbolos (`isTierFailure`, `apiStyleForRequest`, bloco `include_usage`, nome do teste); (b) manter linhas e aceitar nova defasagem. Recommended: (a) — não precisa de manutenção.
- [x] [Review][Patch] Item meta do sweep (2026-10-29) já resolvido segue na lista de pendências [deferred-work.md:14-15]

**Rejected**
- R1/R8: nota de que `include_usage` segue adiado — o item já está em `deferred-work.md`; corrigir exige editar a intent section.
- R4: verificação não checa o entregável — números conferidos em `proxy.go`, sem `TODO`/`FIXME` em `*.go`.
- R5: "único achado" vs outros itens — falso; os outros são de outros épicos e o SMTP na troca de tier está parked (epic.md:73).
- R6: `deferred: []` vs risco residual — itens já estão em `deferred-work.md`.
- R7: "etc.", slug repetido, `ticket: 8` — cosmético.
- R10: diff só de documentação, alegações sem evidência no diff — descritivo, e as alegações foram verificadas.
