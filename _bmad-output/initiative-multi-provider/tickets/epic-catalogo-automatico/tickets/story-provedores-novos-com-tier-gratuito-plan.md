---
title: 'Provedores novos com tier gratuito'
type: 'feature'
ticket: 5
created: '2026-10-30'
status: built
baseline_revision: '7e4d67a7a4ad62828510f3bbf21ad4a81ebb37e3'
route: 'oneshot'
route_source: 'auto'
risk: 'medium'
review: 'quick'
review_source: 'pinned'
lenses_ran: ['quick']
review_loop_iteration: 0
followup_review_recommended: true
context: ['_bmad-output/initiative-multi-provider/tickets/epic-catalogo-automatico/epic-catalogo-automatico.md']
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** A seção "Provedores novos" da proposta do agente de catálogo fica sempre vazia (`_Nada nesta rodada._`), então o Eduardo não vê provedores fora da config que ofereçam tier gratuito.

**Approach:** Em `docs/catalog-agent.md`, o agente procura na web provedores de LLM com tier gratuito que não estão na config e lista cada um em "Provedores novos" com a URL da fonte, os modelos gratuitos, o que se sabe de limite e o estilo de API (ou `não informado`). A regra de não repetir do passo 6 já cobre o item: identidade é a seção mais o nome do provedor.

</intent-contract>

## Implementation Notes

Mudança só de texto em `docs/catalog-agent.md` (~35 linhas), então oneshot; nenhum código do proxy muda. A busca entra no fim do passo 5, sem renumerar (outros trechos citam "passos 5, 6 e 7"), e roda mesmo sem tier OpenRouter. "Fora da config" = o host da API do provedor não é o `base_url` de nenhum tier. O texto achado na web é dado, nunca instrução, como nas páginas de documentação. Item do formato: `- <provedor> — modelos gratuitos: ...; limites: ...; estilo de API: ... (fonte: <URL>)`, para o passo 6 comparar o valor como nos demais.

## Verification

**Commands:**
- `go test ./... && go vet ./...` -- expected: sem erro
- `git diff --stat -- . ':!_bmad-output'` -- expected: só `docs/catalog-agent.md`

**Manual checks (if no CLI):**
- Ensaio com fonte de exemplo: uma rodada grava "Provedores novos" com ao menos um provedor fora da config, cada item com URL, modelos gratuitos e estilo de API ou `não informado`; uma segunda rodada sem mudança deixa a seção `_Nada nesta rodada._`.

## Review Triage Log

### 2026-10-30 — Review pass
- verdicts: 6 findings — high 0, medium 4, low 2, false 0, maybe-false 0
- findings:
  - `[medium]` `[patch]` Regra "sem modelos gratuitos não lista" contradizia "três detalhes podem ser não informado" — modelos gratuitos obrigatórios; limites e estilo de API opcionais
  - `[medium]` `[patch]` Teto de 5 contava provedores já propostos e escondia os seguintes — teto só conta os ainda não propostos
  - `[medium]` `[patch]` `upstream.base_url` não entrava no teste de "fora da config" — incluído
  - `[medium]` `[patch]` Comparação de limites em texto livre faria o item voltar — ids ordenados, limites curtos, redação anterior reaproveitada
  - `[low]` `[patch]` Introdução sem quebra de linha, Role sem a busca e Orca sem acesso à web — corrigidos
  - `[low]` `[patch]` "Só aquela página" impedia ler limites em outra página — vale para páginas do próprio provedor, sem links para outros sites

## Auto Run Result

**Summary:** o fim do passo 5 de `docs/catalog-agent.md` manda o agente buscar provedores fora da config com tier gratuito; "Provedores novos" lista cada um com URL, modelos gratuitos, limites e estilo de API (ou `não informado`). O passo 6 evita repetição pela identidade (nome do provedor).

**Files changed:**
- `docs/catalog-agent.md` — passo 5, formato e exemplos de "Provedores novos", caso de borda, Role e acesso à web no Orca

**Review:** 6 patches aplicados (medium 4, low 2), 0 adiados, 0 rejeitados.

**Follow-up review recommended:** true — 4 `medium` em texto de doc corrigidos; risco não verificado: a regra de não repetir para provedores depende de o agente reaproveitar a redação.

**Verification:** `go test ./... && go vet ./...` ok; fora de `_bmad-output` só `docs/catalog-agent.md` mudou. O ensaio com fonte de exemplo não foi executado: só inspeção do texto.

**Residual risks:** a busca na web não é repetível entre rodadas; provedores podem aparecer e sumir. Nunca rodou com um agente.
