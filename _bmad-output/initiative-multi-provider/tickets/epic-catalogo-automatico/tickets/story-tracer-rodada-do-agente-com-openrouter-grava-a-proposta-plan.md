---
title: 'Tracer: rodada do agente com OpenRouter grava a proposta'
type: 'chore'
ticket: 'story-tracer-rodada-do-agente-com-openrouter-grava-a-proposta'
created: '2026-10-30'
status: 'built'
baseline_revision: '1a429eda028d990d517325b7b2a22d4939b85553'
route: 'full'
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

**Problem:** O Eduardo não tem como saber de modelos novos, mudanças de preço e de gratuidade nos tiers OpenRouter antes de gastar sem querer.

**Approach:** Escrever `docs/catalog-agent.md`: as instruções que o Eduardo cola num agente do Orca. Uma rodada lê `tiers` e `prices` da config (só leitura), consulta a API de modelos do OpenRouter e grava uma proposta em Markdown. Sem código novo no proxy.

## Boundaries & Constraints

**Always:** O agente só lê a config e nunca a altera. Cada item da proposta traz a URL da fonte. Um arquivo por rodada, com data e hora no nome, em `catalog-proposals/` dentro do diretório que o Eduardo define no agente (padrão: ao lado do `state_file`, quando o agente o alcança). Um tier é do OpenRouter quando o host do `base_url` é `openrouter.ai`. Em item de preço ou gratuidade, a direção (gratuito para pago, pago para gratuito) com o valor antigo e o novo. O formato fixa uma seção por tipo: modelos novos, preço, gratuidade, limites, provedores novos (as duas últimas ficam vazias nesta story, para as demais estenderem). Em Markdown, para uma pessoa ler.

**Never:** Escrever na config, no `state_file` ou no `usage_file`. Mexer em código Go, config de tiers ou `prices`. Detectar item já proposto, hashes, guarda de roteamento, páginas de documentação de outros provedores, aviso no Telegram, provedores fora da config e agendamento: são as stories 4.2 a 4.7.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Rodada normal | Config com tier OpenRouter; API responde | Arquivo novo em `catalog-proposals/` com as seções, cada item com URL | Sem erro |
| Sem tier OpenRouter | Nenhum `base_url` com host `openrouter.ai` | Proposta com as seções vazias e a nota "nenhum tier OpenRouter na config" | Sem erro |
| API indisponível | Consulta falha ou responde não-2xx | Proposta registra "OpenRouter indisponível" e o motivo, sem inventar itens | Rodada termina, config intacta |

</intent-contract>

## Code Map

- `docs/catalog-agent.md` -- novo; as instruções do agente (única entrega)
- `docs/configuration.md:42-118` -- exemplo YAML e tabela de campos do tier (`name`, `base_url`, `free`, `models.<nome>.id`); o agente lê esses campos
- `config.go:223-232` (`yamlTier`), `config.go:63-76` (`Prices`, `ModelPrice`: USD por 1M tokens, `input`/`output`) -- fonte da verdade do formato; não alterar
- `README.md:163-169` -- lista de documentos; acrescentar o novo link
- API: `GET https://openrouter.ai/api/v1/models`, cada modelo com `id` e `pricing.prompt`/`pricing.completion` em USD por token (string); multiplicar por 1.000.000 para comparar com `prices`

## Tasks & Acceptance

**Execution:**
- [ ] `docs/catalog-agent.md` -- escrever, em inglês como os demais docs: papel do agente e a regra "só propõe"; entradas (caminho da config, diretório das propostas); passos da rodada (ler `tiers`/`prices`, achar tiers OpenRouter pelo host, consultar a API, comparar modelo a modelo com o `id` mapeado e com `prices`); regra de gratuidade (`prompt` e `completion` iguais a 0); formato da proposta (título com data/hora, seções em português: Modelos novos, Preço, Gratuidade, Limites, Provedores novos; item = linha com URL da fonte; direção com valor antigo e novo); nome do arquivo `catalog-proposals/AAAA-MM-DDTHH-MM.md`; casos de borda da matriz; passo do Eduardo no Orca (criar o agente, config só para leitura) -- tracer que fixa o formato
- [ ] `README.md` -- acrescentar `- [Catalog agent](docs/catalog-agent.md)` à lista de documentos -- descoberta

**Acceptance Criteria:**
- Given o documento, when o Eduardo o cola num agente do Orca com uma config de exemplo, then o agente tem passos suficientes para gravar um arquivo em `catalog-proposals/` com as seções de modelos novos, preço e gratuidade, cada item com URL, sem tocar na config.
- Given uma mudança de preço ou de gratuidade, when o agente a registra, then o item traz a direção, o valor antigo e o novo.
- Given `git diff`, when a story termina, then só `docs/catalog-agent.md` e `README.md` mudaram.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-30 — Review pass
- verdicts: 9 findings — high 0, medium 5, low 4, false 0, maybe-false 0
- findings:
  - `[low]` `[patch]` Nome do arquivo colide na mesma rodada de minuto — sufixo `-2`, `-3` adicionado
  - `[medium]` `[patch]` Valor antigo indefinido na mudança de gratuidade — `US$ 0` / `sem preço na config` definidos
  - `[medium]` `[patch]` Regras de Preço e Gratuidade se sobrepõem — mudança de gratuidade só em Gratuidade
  - `[low]` `[patch]` Primeira rodada lista o catálogo todo — nota adicionada
  - `[medium]` `[patch]` Preço `-1` ou ausente viraria mudança de preço — modelo pulado e listado como `preço variável ou ausente`
  - `[medium]` `[patch]` 2xx com corpo inválido e posição das notas — linha de borda e posição definidas
  - `[low]` `[patch]` Tier 0 ignorado em silêncio — dito como fora de escopo
  - `[medium]` `[patch]` Escrita no diretório do `state_file` — escrita só em `catalog-proposals/`
  - `[low]` `[patch]` Item de modelo novo com preço 0 ou sem preço — formato definido

## Design Notes

"Novo" é o que difere da config atual (`tiers`, `prices`): modelo do OpenRouter que a config não mapeia (por `id`), preço diferente do de `prices`, gratuidade diferente da do tier (`free`). Exemplo de item:

`- anthropic/foo — gratuito para pago: US$ 0 → US$ 0,50 in / 1,50 out por 1M (fonte: https://openrouter.ai/api/v1/models)`

## Verification

**Commands:**
- `git diff --stat main -- . ':!_bmad-output'` -- expected: só `docs/catalog-agent.md` e `README.md`
- `go build ./... && go vet ./...` -- expected: sem erro (nenhum código mudou)

**Manual checks (if no CLI):**
- `docs/catalog-agent.md` cobre cada linha da matriz e cada campo do formato; o verify real (rodar no Orca) é do Eduardo (`hitl`).

## Auto Run Result

**Summary:** `docs/catalog-agent.md` com as instruções do agente de catálogo (rodada com OpenRouter até a proposta em Markdown) e link no README. Sem código Go.

**Files changed:**
- `docs/catalog-agent.md` — novo; papel, entradas, passos da rodada, formato da proposta, bordas, passo no Orca
- `README.md` — link na lista de documentos

**Review:** 9 patches aplicados (medium 5, low 4), 0 adiados, 0 rejeitados.

**Follow-up review recommended:** false (nenhum `high`; 5 `medium` aplicados, todos em texto de doc, cobertos pelo ajuste).

**Verification:** `go build ./... && go vet ./...` ok. O verify real (rodar o agente no Orca com config de exemplo) é do Eduardo (`hitl`) e não foi feito.

**Residual risks:** o documento nunca foi executado por um agente; o formato da API do OpenRouter (`pricing` em USD por token, string) vem do conhecimento prévio, não de chamada real.
