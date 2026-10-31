---
title: 'Limites novos e propostas sem repetição'
type: 'feature'
ticket: 4
created: '2026-10-30'
status: done
baseline_revision: 'aef6de5a3eb00906de123d2dd523efde7eea4af3'
route: 'oneshot'
route_source: 'auto'
risk: 'medium'
review: 'quick'
review_source: 'pinned'
lenses_ran: ['quick']
review_loop_iteration: 0
followup_review_recommended: false
context: ['_bmad-output/initiative-multi-provider/tickets/epic-catalogo-automatico/epic-catalogo-automatico.md']
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** A cada rodada o agente de catálogo repropõe tudo o que já propôs (a primeira rodada lista todo modelo que a config não mapeia, e uma gratuidade perdida aparece de novo toda vez), e um limite lido da página não é comparado com o da rodada anterior.

**Approach:** Em `docs/catalog-agent.md`, antes de gravar, o agente lê as propostas anteriores em `catalog-proposals/` e descarta o item cujo valor é igual ao último proposto com a mesma identidade. Item novo ou com valor diferente sai, com `(antes proposto: <valor>)` quando já tinha sido proposto; o limite ganha a forma `<nome do limite>: <valor>` para poder ser comparado.

</intent-contract>

## Implementation Notes

Mudança só de texto em `docs/catalog-agent.md` (~45 linhas), então oneshot; nenhum código do proxy muda. A regra vai dentro do passo 6, sem renumerar passos (outros trechos citam "passos 5, 6 e 7"). Compara com todas as propostas completas, da mais nova para a mais antiga, e não só a da rodada anterior: se só a anterior valesse, um item suprimido na rodada 2 voltaria na rodada 3. Item `não verificado` segue a regra comum (mesmo texto = não repete), como diz o ticket.

## Review Triage Log

### 2026-10-30 — Review pass
- verdicts: 6 findings — high 0, medium 2, low 4, false 0, maybe-false 0
- findings:
  - `[medium]` `[patch]` Valor de "Limites" incluía o nome do limite — definido como o que vem depois do nome
  - `[medium]` `[patch]` Comparação por texto livre faria itens voltarem — passa a comparar sentido (números arredondados, mesmo nome, `não verificado` sem o motivo) e reusar a redação anterior
  - `[low]` `[patch]` Texto das propostas anteriores lido sem aviso de injeção — dito que é dado
  - `[low]` `[patch]` Pasta que não lista — tratada como sem proposta anterior
  - `[low]` `[reject]` Ordem por nome na virada do horário de verão — raro, o conserto acrescenta regra
  - `[low]` `[reject]` Linha longa — corrigida junto da reescrita do bloco

## Verification

**Commands:**
- `go test ./... && go vet ./...` -- expected: sem erro
- `git diff --stat -- . ':!_bmad-output'` -- expected: só `docs/catalog-agent.md`

**Manual checks (if no CLI):**
- Ensaio com fonte de exemplo: duas rodadas sem mudança dão proposta sem itens na segunda; mudar um preço em `prices` e um limite da proposta anterior traz só esses dois itens, com valor antigo e novo.

## Auto Run Result

**Summary:** o passo 6 de `docs/catalog-agent.md` lê as propostas anteriores (da mais nova para a mais antiga) e descarta o item com a mesma identidade e valor igual; item mudado sai com `(antes proposto: <valor>)`. "Limites" ganha a forma `<nome do limite>: <valor>`.

**Files changed:**
- `docs/catalog-agent.md` — passo 6, formato de "Limites", exemplos, bordas e acesso de leitura no Orca

**Review:** 4 patches aplicados (medium 2, low 2), 0 adiados, 2 rejeitados (low).

**Follow-up review recommended:** false (nenhum `high`; 2 `medium` em texto de doc, já corrigidos).

**Verification:** `go test ./... && go vet ./...` ok; fora de `_bmad-output` só `docs/catalog-agent.md` mudou. O ensaio com fonte de exemplo não foi executado por um agente: só inspeção do texto.

**Residual risks:** a regra depende de o agente comparar sentido e reusar a redação anterior; nunca rodou. Item `não verificado` persistente deixa de aparecer a partir da segunda rodada.

## Code Review

### 2026-10-30
Revisão: 1 `decision-needed`, 2 `patch`, 0 `defer`, 13 rejeitados.

- [x] [Review][Decision] (resolvido: ensaio rodado em sandbox; rodadas 1-3 ok, limites não testados por 404 da página) Ensaio do Verify (duas rodadas sem mudança; preço e limite alterados) nunca foi executado — a regra só foi lida, e o plano admite que "nunca rodou". Options: (a) rodar o ensaio com um agente e fonte de exemplo agora; (b) aceitar e deixar registrado como risco; (c) adiar para um teste de fumaça na integração do épico. Recommended: (a) — é o Verify do ticket e a única checagem que exercita a mudança.
- [x] [Review][Patch] Exemplo de "Limites" mostra duas linhas da mesma identidade (60 e 120) no mesmo bloco, contra a regra do passo 6 [docs/catalog-agent.md:257]
- [x] [Review][Patch] Formatos de "Preço" e "Modelos novos" não mostram o sufixo `(antes proposto: ...)` que a regra do passo 6 e a tabela de bordas aplicam [docs/catalog-agent.md:232]

**Rejected**
- Item `não verificado` persistente some a partir da segunda rodada (2 achados): é o que o ticket pede; risco já registrado no plano.
- Última proposta não mostra pendências antigas: inerente a não repetir itens.
- Histórico sem teto / arquivar ressuscita itens (2 achados): crescimento lento; um teto acrescenta regra.
- Nome de limite renomeado ou ambíguo: a regra "mesmo nome" e reusar a redação já cobrem.
- Valor volta após ausência e é suprimido: a pessoa já viu esse valor; o conserto acrescenta regra.
- Limite sem `: ` ou no formato antigo: afeta só a transição e só gera itens a mais, nunca suprime.
- Todos os arquivos pulados sem explicação, ordenação de sufixo `-10` vs `-9`: raro; o conserto acrescenta regra.
- `(antes proposto)` aninhado: falso, o valor já é definido sem `(antes proposto)` e `(fonte)`.
- Limite em `tiers` sem comparação com a config: falso, limite não tem campo na config (passo 4).
- Contexto, tool calling e disponibilidade sem mudança no diff: falso para este diff, vêm de passos anteriores.
