---
id: 5
type: epic
title: "O proxy não vaza erro do upstream e rejeita config inválida"
parent: initiative-multi-provider
covers: [R7]
sources: [_bmad-output/initiative-multi-provider/deferred-work.md]
companions: []
after: []
refined: true
risk: medium
---

# O proxy não vaza erro do upstream e rejeita config inválida

## Description

Fecha as pendências de código que os code reviews dos épicos 2 e 3 adiaram em `deferred-work.md`. Nenhuma é um recurso novo, exceto o aviso SMTP na troca de tier. A única que expõe dado é o erro de transporte devolvido cru ao cliente, que vaza a URL do upstream.

## Outcome

Para o Eduardo: nenhum cliente do proxy vê a URL do upstream, e uma config com valor inválido é recusada na partida em vez de ignorada; o sinal é um teste de cada caso.

## Requirements

- R1 (deferred-work, review da separação do main.go): Um erro de transporte ao upstream chega ao cliente por `writeAPIError`, sem a URL do upstream. Verificação: teste com upstream inalcançável que procura a URL na resposta.
- R2 (parent R7): Um 404 de modelo inexistente num tier faz a request seguir ao próximo tier, sem cooldown. Verificação: teste de failover, trocando o que hoje afirma `TestFailover400And404MissingModelPassThroughWithoutCooldown`.
- R3 (deferred-work, review dos estilos de API): `/v1/completions` é classificado como OpenAI e sai com o cabeçalho de autenticação do estilo OpenAI. Verificação: teste de `apiStyleForRequest`.
- R4 (deferred-work, review do teto diário): `include_usage` só é injetado em `/chat/completions`, se o upstream rejeitar `stream_options.include_usage` nos outros caminhos de OpenAI. Verificação: chamada real ao upstream em `/v1/responses`, depois teste do caminho.
- R5 (deferred-work, review da separação do main.go): `MAX_REQUEST_BODY_BYTES` e `SMTP_PORT` inválidos são rejeitados por `validateConfig`, como as durações. Verificação: teste de config com cada valor inválido.
- R6 (deferred-work, review da separação do main.go): `usage_url` e `usage_cache_ttl` são aceitos no YAML, com a variável de ambiente ainda valendo. Verificação: teste de config pelo YAML.
- R7 (deferred-work, review do refactor sweep): Na troca de um tier para outro o Eduardo recebe um aviso SMTP, uma vez por episódio. Verificação: teste com o tier 0 esgotado e um extra atendendo.

## Constraints

- A config e os agentes Hermes não mudam: mesmo URL e mesma `PROXY_API_KEY` (parent, Boundaries).
- O 404 que segue ao próximo tier não coloca o tier em cooldown (deferred-work, review do failover).

## Done when

- Um erro de transporte não mostra a URL do upstream ao cliente.
- Um 404 de modelo inexistente é atendido pelo próximo tier mapeado.
- `/v1/completions` sai como OpenAI e `include_usage` só vai onde o upstream aceita.
- Uma config com `MAX_REQUEST_BODY_BYTES` ou `SMTP_PORT` inválido é rejeitada na partida; `usage_url` e `usage_cache_ttl` valem pelo YAML.
- A troca de tier gera um aviso SMTP por episódio.

## Boundaries

- Fora: os verifies `hitl` do épico de catálogo (4.3, 4.4, 4.5, 4.7), que ficam com o Eduardo.
- Touch point: `proxyV1`, `isTierFailure`, `apiStyleForRequest` e `withIncludeUsage` em `proxy.go`, e `validateConfig` e `yamlConfig` em `config.go` — owner: este épico
- Touch point: o alerta SMTP existente do teto diário — owner: epic-relatorio-de-uso-e-custo
- Must not change: o URL e a `PROXY_API_KEY` que os agentes usam; o formato da config de tiers.

## References

- parent — _bmad-output/initiative-multi-provider/initiative-multi-provider.md, section Requirements (R7)
- deferred-work — _bmad-output/initiative-multi-provider/deferred-work.md
- code — `proxy.go`, `config.go`, `smtp.go`

## Notes

- Decision (2026-10-31): as pendências de código viram um épico, não bugs avulsos no backlog (Eduardo).
- Decision (2026-10-31): o 404 de modelo inexistente segue ao próximo tier sem cooldown, e o teste que afirma o contrário muda (Eduardo).
- Decision (2026-10-31): o aviso SMTP na troca de tier vale uma vez por episódio, como o Telegram do R16 (Eduardo).
- Decision (2026-10-31): `MAX_REQUEST_BODY_BYTES`/`SMTP_PORT` e `usage_url`/`usage_cache_ttl` ficam numa story só, porque tocam o mesmo `validateConfig`.
- Decision (2026-10-31): a story 1 (erro de transporte sem URL) é o tracer bullet: é a única que expõe dado. A faixa de `proxy.go` segue em ordem; a de `config.go` corre em paralelo; o Refactor sweep fecha.
- Assumption: o item do `include_usage` só vira mudança se o upstream rejeitar o campo em `/v1/responses`; a story 4 abre com essa verificação.
