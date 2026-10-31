# Deferred work

## Deferred from: code review of story-separar-main-go-em-arquivos-do-package-main-plan (2026-10-29)
- `proxy.go:40`: `proxyV1` devolve `reqErr.Error()` cru ao cliente em erro de transporte; vaza a URL do upstream e ignora `writeAPIError`. Tratar na story de failover.
- `config.go:223,262`: `MAX_REQUEST_BODY_BYTES` e `SMTP_PORT` inválidos são ignorados em silêncio, ao contrário das durações, que o `validateConfig` rejeita. Tratar na story de config dos tiers.
- `config.go:96`: `yamlConfig` não tem `usage_url` nem `usage_cache_ttl`; só por variável de ambiente.

## Deferred from: code review of story-failover-por-status-do-upstream-com-cooldown-por-tier-plan (2026-10-29)
- 404 de modelo inexistente em um tier não segue ao próximo tier (`isTierFailure` em proxy.go). Epic R7 diz que 404 segue ao próximo; o verify da story só pede "sem cooldown". Severidade medium não verificada; decidir a intenção resolve. Segue aberto em 2026-10-30: 404 que cita o modelo com "not found" etc. continua sem passar ao próximo tier (`TestFailover400And404MissingModelPassThroughWithoutCooldown`).

## Deferred from: code review of story-estilos-de-api-por-tier-plan (2026-10-29)
- `apiStyleForRequest` (proxy.go): testa `HasPrefix(path, "/complete")` e classifica `/v1/completions` (OpenAI legado) como anthropic; a request sai com `x-api-key` e pula tiers só OpenAI. Pré-existente.

## Deferred from: code review of story-refactor-sweep-plan (2026-10-29), item SMTP
- Aviso SMTP na troca de tier: hoje o SMTP só dispara quando todas as chaves do tier 0 esgotam. Citado pelo sweep como fora de escopo; pede story nova.

## Deferred from: code review of story-teto-diario-em-dinheiro-para-tiers-pagos-plan (2026-10-30)
- `include_usage` é injetado em todo request OpenAI com `stream: true` num tier pago, inclusive caminhos como `/v1/responses` (`withIncludeUsage` em proxy.go). Severidade média, não verificada. Resolve ao ver se o upstream aceita `stream_options.include_usage` nesse caminho; se rejeitar, limitar a `/chat/completions`.

## Deferred from: code review of story-provedores-da-config-fora-do-openrouter-pela-documentacao-plan (2026-10-30)
- Verify de ponta a ponta da 4.3 (hitl): rodar o agente com um tier real na tabela e com a página trocada por uma ilegível. Hoje a tabela só tem `exemplo-zai`, então nada disso foi observado.

## Deferred from: code review of story-limites-novos-e-propostas-sem-repeticao-plan (2026-10-30)
- Verify de limites da 4.4: o ensaio em sandbox cobriu as rodadas sem mudança e com preço alterado, mas `https://docs.z.ai/pricing` dá 404 e nenhuma rodada leu limites. Falta rodar com uma página legível de um tier fora do OpenRouter e conferir o nome do limite na identidade e o `(antes proposto: ...)` em "Limites".

## Deferred from: code review of story-provedores-novos-com-tier-gratuito-plan (2026-10-31)
- Verify de ponta a ponta da 4.5: nenhuma rodada real gravou "Provedores novos"; só houve inspeção do texto. Falta rodar o agente com busca na web e conferir URL, modelos gratuitos e estilo de API, e uma segunda rodada sem repetição.
- Definição de "tier gratuito" na busca de provedores: o texto não separa cota recorrente de crédito de teste, cartão obrigatório ou restrição de região, e o estilo de API não prevê "compatível com OpenAI e Anthropic". Recomendado: só cota gratuita recorrente.
