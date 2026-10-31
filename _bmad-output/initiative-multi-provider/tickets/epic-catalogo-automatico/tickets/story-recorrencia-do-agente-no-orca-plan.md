---
title: 'Recorrência do agente no Orca'
type: 'chore'
ticket: 7
created: '2026-10-31'
status: done
baseline_revision: '28f2d6ff2e6eec35549af2ee906706d4aa37961d'
route: 'oneshot'
route_source: 'auto'
risk: 'low'
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

**Problem:** O agente do catálogo só roda quando o Eduardo o dispara à mão (`docs/catalog-agent.md` termina com "Scheduling is not part of this version"), então uma mudança de gratuidade pode passar sem aviso.

**Approach:** `docs/catalog-agent.md` ganha uma seção de recorrência com o intervalo recomendado (diário) e o comando `orca automations create` que o Eduardo roda para agendar a rodada; o repo não agenda nada.

</intent-contract>

## Implementation Notes

Só texto em `docs/catalog-agent.md` (~20 linhas), então oneshot. O Orca tem `orca automations create --name --trigger --prompt --provider --workspace [--timezone] [--reuse-session|--fresh-session]`; o prompt da automação dá os dois caminhos que o agente pediria (config e diretório de propostas), porque a rodada recorrente não tem ninguém para responder. Intervalo diário: gratuidade perdida é o aviso que evita gasto, e a rodada só escreve arquivo e, no máximo, uma mensagem. Substitui a linha "Scheduling is not part of this version." do passo 5 de "Setting it up in Orca". O verify (Orca dispara sozinho e uma proposta nova aparece) é `hitl`, do Eduardo.

## Verification

**Commands:**
- `go test ./... && go vet ./...` -- expected: sem erro
- `git diff --stat -- . ':!_bmad-output'` -- expected: só `docs/catalog-agent.md`

**Manual checks (if no CLI):**
- Inspeção do texto: a seção traz o intervalo, o comando completo com os flags que existem em `orca automations create --help`, o aviso de fusos/horário e como conferir (`orca automations runs`); nada diz que o repo agenda.
- (hitl) O Orca dispara uma rodada sem ação do Eduardo e uma proposta nova aparece em `catalog-proposals/` com data e hora posteriores à anterior.

## Review Triage Log

### 2026-10-31 — Review pass
- verdicts: 5 findings — high 0, medium 4, low 1, false 0, maybe-false 0
- findings:
  - `[medium]` `[patch]` A automação lê o `docs/catalog-agent.md` do workspace, não o texto colado, então a tabela de páginas precisa estar lá — nota "Which copy runs"
  - `[medium]` `[patch]` Sem flag de permissões no `orca automations create` — nota "Access": config só leitura no disco, `state_file`/`usage_file` sem escrita, ferramentas pré-autorizadas
  - `[medium]` `[patch]` Faltava o aviso de fuso/horário e de máquina desligada — nota "Time" com `--missed-run-grace-minutes`
  - `[medium]` `[patch]` Falha de rodada não grava arquivo e ninguém lê o chat — nota "Knowing it worked" manda olhar `orca automations runs` se passar um dia sem arquivo novo
  - `[low]` `[patch]` "Inputs" não dizia que a automação traz os caminhos no prompt — frase acrescentada

## Auto Run Result

**Summary:** `docs/catalog-agent.md` ganha a seção "Running it on a schedule": intervalo diário recomendado e o comando `orca automations create` (flags conferidos no `--help`), mais notas de qual cópia roda, acesso, fuso e como saber que funcionou. O passo 6 de "Setting it up in Orca" aponta para ela.

**Files changed:**
- `docs/catalog-agent.md` — seção de recorrência, passo 6 e nota em "Inputs"

**Review:** 5 patches aplicados (medium 4, low 1), 0 adiados, 0 rejeitados.

**Follow-up review recommended:** true — 4 `medium` corrigidos em texto; risco não verificado: o Orca aceitar `--workspace path:<dir>` e rodar o agente sem prompts de permissão.

**Verification:** `go test ./... && go vet ./...` ok; só `docs/catalog-agent.md` mudou fora de `_bmad-output`. A rodada real pelo Orca (verify `hitl`) não foi executada.

**Residual risks:** o comando nunca foi executado; falha de rodada só aparece no histórico do Orca.

## Code Review

### 2026-10-31
0 decision-needed, 6 patch, 1 defer, 6 rejected.

- [x] [Review][Patch] Access: dar escrita ao diretório de propostas (fora do workspace) e criá-lo antes da primeira rodada [docs/catalog-agent.md:133]
- [x] [Review][Patch] Pré-autorização de busca/fetch sem nota de que a rodada só escreve proposta, state e usage [docs/catalog-agent.md:133]
- [x] [Review][Patch] Comando de exemplo sem `--fresh-session` e `--missed-run-grace-minutes` (sem valor sugerido) [docs/catalog-agent.md:115]
- [x] [Review][Patch] "Knowing it worked" assume intervalo diário; a seção também oferece semanal [docs/catalog-agent.md:140]
- [x] [Review][Patch] `--prompt` entre aspas duplas quebra com caminhos com espaço ou `$` [docs/catalog-agent.md:121]
- [x] [Review][Patch] "Run this from the repo checkout" contradiz o `--workspace path:` absoluto; linha de Access com mais de 80 colunas [docs/catalog-agent.md:112,135]
- [x] [Review][Defer] `--workspace path:<dir>` aceito pelo Orca não confirmado [docs/catalog-agent.md:120] — deferred: rodar o comando uma vez (verify hitl) resolve

#### Rejected
- Telegram sem credenciais na rodada: falso, `bot_token`/`chat_id` vêm da config (linha 65).
- `run`/`runs` inconsistentes e `--time` fora da lista: falso, `orca automations --help` confirma os três.
- Linha "Scheduling is not part of this version" removida: falso, a seção nova repete a garantia (107-109).
- Guarda do R9 depende de passos manuais: falso, está documentado no passo 4 e em "Access".
- Pausar/remover a automação e custo: fora de escopo (Orca já tem `--disabled`).
- `chmod a-w` não vale para o dono, `go test` como verificação, tabela local perdida: baixo, o doc já oferece mount somente leitura e manda manter a branch; o resto edita o plano.
