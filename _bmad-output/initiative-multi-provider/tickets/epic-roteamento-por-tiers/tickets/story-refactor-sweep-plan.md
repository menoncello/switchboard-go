---
title: 'Refactor sweep'
type: 'refactor'
ticket: 9
created: '2026-10-29'
status: 'done'
baseline_revision: '36eed6dd046a9099534c6fc3d55fcce72e51c0f7'
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

**Problem:** As stories do épico deixaram cleanup adiado em `deferred-work.md` e nos achados de revisão: código sem chamador, uma checagem de erro por texto e planos com checkboxes e referências desatualizados.

**Approach:** Só os itens que não mudam comportamento: remover `KeyManager.AdvanceOnExhaustion` e `(*App).validateConfigAndPrint` (sem chamadores), trocar o `strings.Contains` do corpo grande em `serveProxy` por `errors.As` com `*http.MaxBytesError`, e acertar os planos do épico. Os itens que mudam comportamento (erro de transporte cru, `MAX_REQUEST_BODY_BYTES` e `SMTP_PORT` inválidos, `usage_url` no YAML, `/v1/completions`, 404 de modelo, aviso SMTP na troca de tier) ficam fora: viram story nova.

</intent-contract>

## Implementation Notes

Oneshot: ~15 linhas de Go (`keys.go`, `server.go`, `proxy.go`) e edição de texto nos planos de `tickets/epic-roteamento-por-tiers/tickets/`. Nos planos com status `done`, marcar `[x]` nas tasks de execução; no plano de elegibilidade, `proxyV1` vira `serveProxy` onde se refere ao laço atual e a task de fixtures rejeitada sai; no plano de persistir o cooldown, "8 testes novos" vira 9 e "última gravação bem-sucedida" sai do Design Notes. `advanceLocked` continua (tem outros chamadores).

Feito: removidos `AdvanceOnExhaustion` (`keys.go`) e `validateConfigAndPrint` (`server.go`, mais o import `log` que ficou sem uso); `serveProxy` usa `errors.As` com `*http.MaxBytesError`. Planos: tasks `[ ]` viraram `[x]` em 7 planos `done`; elegibilidade ganhou `serveProxy` e a nota do fixture rejeitado; persistir perdeu o "8 testes" (eram 11 `TestState*`, e "última gravação bem-sucedida" já descreve o `lastState` atual, então ficou). Sem mudança de comportamento; `go build`, `go vet` e `go test -race` passam.

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 4 findings — high 0, medium 0, low 4, false 0, maybe-false 0
- findings:
  - `[low]` `patch` plano do Telegram com `[ ] [Review][Patch]` da 1ª rodada, já cobertos ou rejeitados na 2ª — marcados `[x]`
  - `[low]` `patch` task do plano de elegibilidade ainda dizia `proxyV1` — trocado por `serveProxy`
  - `[low]` `patch` `deferred-work.md` ainda listava os itens resolvidos (código morto, `strings.Contains`, dois "plano inconsistente") — removidos
  - `[low]` `reject` plano de persistir com contagem e frase em desacordo com a nota — a contagem sem número é correta (11 testes) e a frase descreve o `lastState` atual
  - `[info]` sem `AGENTS.md`/`CLAUDE.md` no repositório; o revisor não rodou build nem testes (rodaram aqui)

## Auto Run Result

- Mudança: removidos `AdvanceOnExhaustion` e `validateConfigAndPrint`; `serveProxy` usa `errors.As` com `*http.MaxBytesError`; planos do épico com tasks marcadas e referências corrigidas; `deferred-work.md` sem os itens resolvidos.
- Arquivos: `keys.go`, `server.go`, `proxy.go`, planos de `tickets/epic-roteamento-por-tiers/tickets/`, `deferred-work.md`.
- Review: 4 achados (low); 3 patches aplicados, 1 rejeitado.
- Follow-up recomendado: não.
- Verificação: `go build`, `go vet` e `go test -race -count=1 ./...` passam antes e depois dos patches.
- Risco residual: nenhum comportamental. Itens que mudam comportamento ficam em `deferred-work.md` (erro de transporte cru, `MAX_REQUEST_BODY_BYTES`/`SMTP_PORT`, `usage_url` no YAML, `/v1/completions`, 404 de modelo) e pedem story nova.

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test -race -count=1 ./...` -- expected: todos passam
- `rg "AdvanceOnExhaustion|validateConfigAndPrint" --glob '*.go'` -- expected: sem resultados

## Code Review

### 2026-10-29
Review do commit 9f47113: 1 `decision-needed`, 1 `patch`, 1 `defer`, 8 rejeitados.

- [x] [Review][Patch] (decidido: registrar) Item "aviso SMTP na troca de tier" citado como adiado sem entrada em `deferred-work.md` — a Intent e o Risco residual dizem que vira story nova, mas o item não está registrado em nenhum lugar de `_bmad-output`. Options: (a) registrar em `deferred-work.md`; (b) tirar a menção do plano; (c) apontar onde já está registrado. Recommended: (a) — a story nova prometida não pode depender de memória.
- [x] [Review][Patch] Code Map do plano de elegibilidade ainda manda ajustar os fixtures rejeitados [story-elegibilidade-por-contexto-minimo-e-tool-calling-plan.md:53]
- [x] [Review][Defer] Referências de linha em `deferred-work.md` defasadas (`proxy.go:213-223`, `proxy.go:352`, hoje 372) [deferred-work.md] — deferred: drift pré-existente, atualizar na story que tratar esses itens

**Rejected**
- Sem teste do 413 — `TestRequestTooLargeReturns413` e `TestRequestBodyTooLargeStill413` cobrem.
- `proxyV1` ainda citado em outros planos — `proxyV1` existe em `proxy.go:75` como wrapper; as citações são históricas.
- Status `built` em vez de `done` — transição de status é do fechamento do ticket.
- Verification do sweep sem `rg` de `[ ]`/`proxyV1` (low) — mais checagem para ganho raro.
- Plano de persistir com "8 testes"/frase "última gravação" (2 achados, low) — o "Feito" registra a rejeição com motivo; o fix editaria a intent.
- `errors.As` sem teste novo — testes existentes cobrem.
