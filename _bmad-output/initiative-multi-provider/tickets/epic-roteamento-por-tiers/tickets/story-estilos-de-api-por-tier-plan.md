---
title: 'Estilos de API por tier'
type: 'feature'
ticket: 5
created: '2026-10-29'
status: 'built'
baseline_revision: 'efe0c4920e99665090f6484db1076c213ffc9585'
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
      `apiStyleForRequest` classifica `/v1/completions` (OpenAI legado) como anthropic, porque testa o prefixo `/complete`.
    evidence: |-
      proxy.go: `strings.HasPrefix(path, "/complete")` depois de `TrimPrefix(path, "/v1")`; `/completions` casa. A request leva x-api-key e o filtro de estilo dos tiers usa anthropic.
    location: >-
      proxy.go:apiStyleForRequest
    severity: medium
---

<intent-contract>

## Intent

**Problem:** O código já pula o tier que não fala o estilo da request (`tierModelID`) e já autentica pelo estilo (`doUpstream`), mas só há teste para um tier de dois estilos com request Anthropic e a doc não tem uma seção sobre estilos; falta provar o resto do verify do ticket (tier de dois estilos com request OpenAI, 429 local em formato Anthropic).

**Approach:** Testes novos em `main_test.go` para as três situações do verify e uma subseção "API styles" em `docs/configuration.md`; nenhuma mudança em `proxy.go` a menos que um teste mostre defeito.

</intent-contract>

## Implementation Notes

Oneshot: o comportamento já existe (estilos em `TierConfig.Styles`, default só OpenAI, `tierModelID` pula, `doUpstream` escolhe `x-api-key` ou `Bearer`); a story fecha com testes e doc.

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 4 findings — high 0, medium 1, low 3, false 0, maybe-false 0
- findings:
  - `[low]` `patch` doc dizia que sem tier compatível sempre sai o 429 local — reescrita: só quando nenhum tier devolveu resposta (doc corrigida)
  - `[medium]` `defer` `/v1/completions` cai no estilo anthropic — pré-existente (proxy.go, `apiStyleForRequest`), a doc só descreve o prefixo; registrado em `deferred`
  - `[low]` `reject` `TestTierAuthFollowsRequestStyle` sem checar status — o length e os valores de `auths` já falham se a request não chega ao t2
  - `[low]` `reject` teste do 429 sem Content-Type/mensagem — `type:"error"` e `rate_limit_error` só existem no 429 local (o `quotaBody` do tier 0 não tem), e `hits[1]==0` prova o skip

## Auto Run Result

- Mudança: testes do estilo por tier (tier de dois estilos autentica pelo estilo da request; sem tier compatível, 429 local no formato Anthropic) e subseção "API styles" em `docs/configuration.md`. `proxy.go` não mudou: o comportamento já existia.
- Arquivos: `main_test.go` (2 testes), `docs/configuration.md` (seção nova).
- Review: 4 achados; 1 patch (doc), 1 adiado (medium), 2 rejeitados (low).
- Follow-up recomendado: não.
- Verificação: `go build`, `go vet`, `go test -race -count=1 ./...` passam.
- Risco residual: `/v1/completions` é tratado como Anthropic (adiado).

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test -race -count=1 ./...` -- expected: todos passam, incluindo os novos
