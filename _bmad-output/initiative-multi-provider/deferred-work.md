# Deferred work

## Deferred from: code review of story-separar-main-go-em-arquivos-do-package-main-plan (2026-10-29)
- `proxy.go:40`: `proxyV1` devolve `reqErr.Error()` cru ao cliente em erro de transporte; vaza a URL do upstream e ignora `writeAPIError`. Tratar na story de failover.
- `config.go:223,262`: `MAX_REQUEST_BODY_BYTES` e `SMTP_PORT` inválidos são ignorados em silêncio, ao contrário das durações, que o `validateConfig` rejeita. Tratar na story de config dos tiers.
- `config.go:96`: `yamlConfig` não tem `usage_url` nem `usage_cache_ttl`; só por variável de ambiente.

## Deferred from: code review of story-failover-por-status-do-upstream-com-cooldown-por-tier-plan (2026-10-29)
- 404 de modelo inexistente em um tier não segue ao próximo tier (proxy.go:213-223). Epic R7 diz que 404 segue ao próximo; o verify da story só pede "sem cooldown". Severidade medium não verificada; decidir a intenção resolve.

## Deferred from: code review of story-estilos-de-api-por-tier-plan (2026-10-29)
- `proxy.go:352`: `apiStyleForRequest` testa `HasPrefix(path, "/complete")` e classifica `/v1/completions` (OpenAI legado) como anthropic; a request sai com `x-api-key` e pula tiers só OpenAI. Pré-existente.

## Deferred from: code review of story-refactor-sweep-plan (2026-10-29)
- `deferred-work.md`: referências de linha dos itens de 404 (`proxy.go:213-223`) e `apiStyleForRequest` (`proxy.go:352`, hoje `proxy.go:372`) estão defasadas; atualizar na story que tratar esses itens. Pré-existente.

## Deferred from: code review of story-refactor-sweep-plan (2026-10-29), item SMTP
- Aviso SMTP na troca de tier: hoje o SMTP só dispara quando todas as chaves do tier 0 esgotam. Citado pelo sweep como fora de escopo; pede story nova.

## Deferred from: code review of story-teto-diario-em-dinheiro-para-tiers-pagos-plan (2026-10-30)
- `include_usage` é injetado em todo request OpenAI com `stream: true` num tier pago, inclusive caminhos como `/v1/responses` (proxy.go:355-358). Severidade média, não verificada. Resolve ao ver se o upstream aceita `stream_options.include_usage` nesse caminho; se rejeitar, limitar a `/chat/completions`.
