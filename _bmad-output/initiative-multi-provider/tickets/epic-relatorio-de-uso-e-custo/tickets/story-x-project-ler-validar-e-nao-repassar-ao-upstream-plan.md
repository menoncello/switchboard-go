---
title: 'X-Project: ler, validar e não repassar ao upstream'
type: 'feature'
ticket: '3.1'
created: '2026-10-29'
status: 'built'
baseline_revision: '8095432d51cccf6bc4d01e42bd1d5f4e727cff52'
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

**Problem:** O proxy não sabe a qual projeto uma request pertence, e o cabeçalho `X-Project` do cliente seguiria para o upstream.

**Approach:** Em `proxyV1`, ler `X-Project`, aceitar até 64 caracteres `[A-Za-z0-9._-]` (ausente ou inválido vira `sem projeto`, a request segue), gravar como `project` na linha de log e remover o cabeçalho em `copyHeaders`, que vale para o tier 0 e para os extras. Documentar o campo em `docs/configuration.md`, seção Request log. Os demais campos do log não mudam.

</intent-contract>

## Implementation Notes

Oneshot: cerca de 25 linhas em `proxy.go`, mais testes e uma linha de doc.

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 1 findings — high 0, medium 0, low 1, false 0, maybe-false 0
- findings:
  - `[low]` `[patch]` `docs/configuration.md` dizia que cabeçalhos da request nunca são logados, o que contradiz `project` — frase reescrita: só `X-Project` é logado, validado.

## Verification

**Commands:**
- `go test ./...` -- expected: verde, com testes novos de `X-Project` válido, ausente, longo e com caractere inválido
- `go vet ./...` -- expected: sem aviso

## Auto Run Result

**Resumo:** o proxy lê `X-Project` (até 64 caracteres `[A-Za-z0-9._-]`; ausente ou inválido vira `sem projeto`), grava `project` no log por request e não repassa o cabeçalho a nenhum tier.

**Arquivos:**
- `proxy.go` -- `requestProject`, campo `project` no log, `X-Project` ignorado em `copyHeaders`
- `main_test.go` -- `TestRequestLogProjectHeader` (válido, ausente, 64, 65, espaço, `/`, tier extra, sem vazamento ao upstream)
- `docs/configuration.md` -- campo `project` na seção Request log e frase sobre cabeçalhos logados

**Revisão:** 1 achado, 1 patch aplicado (doc), 0 adiados, 0 rejeitados.
**Follow-up recomendado:** false.
**Verificação:** `go vet ./...` e `go test ./...` verdes. O `curl` com o proxy rodando não foi feito; o teste cobre o mesmo caminho.
**Risco residual:** nenhum conhecido.
