# Deferred work

## Deferred from: code review of story-separar-main-go-em-arquivos-do-package-main-plan (2026-10-29)
- `proxy.go:40`: `proxyV1` devolve `reqErr.Error()` cru ao cliente em erro de transporte; vaza a URL do upstream e ignora `writeAPIError`. Tratar na story de failover.
- `config.go:223,262`: `MAX_REQUEST_BODY_BYTES` e `SMTP_PORT` inválidos são ignorados em silêncio, ao contrário das durações, que o `validateConfig` rejeita. Tratar na story de config dos tiers.
- `keys.go:105`, `server.go:226`: `KeyManager.AdvanceOnExhaustion` e `(*App).validateConfigAndPrint` não têm chamadores. Remover quando uma story puder mudar código.
- `config.go:96`: `yamlConfig` não tem `usage_url` nem `usage_cache_ttl`; só por variável de ambiente.
- `proxy.go:24`: detecção de body grande por `strings.Contains` no texto do erro; usar `errors.As` com `*http.MaxBytesError`.

## Deferred from: code review of story-failover-por-status-do-upstream-com-cooldown-por-tier-plan (2026-10-29)
- 404 de modelo inexistente em um tier não segue ao próximo tier (proxy.go:213-223). Epic R7 diz que 404 segue ao próximo; o verify da story só pede "sem cooldown". Severidade medium não verificada; decidir a intenção resolve.

## Deferred from: code review of story-estilos-de-api-por-tier-plan (2026-10-29)
- `proxy.go:352`: `apiStyleForRequest` testa `HasPrefix(path, "/complete")` e classifica `/v1/completions` (OpenAI legado) como anthropic; a request sai com `x-api-key` e pula tiers só OpenAI. Pré-existente.

## Deferred from: code review of story-elegibilidade-por-contexto-minimo-e-tool-calling-plan (2026-10-29)
- Plano inconsistente: checkboxes `[ ]` com status built, `proxyV1` no lugar de `serveProxy`, task de fixtures rejeitada ainda listada. Corrigir no fechamento do plano.

## Deferred from: code review of story-persistir-o-cooldown-dos-tiers-plan (2026-10-29)
- Plano inconsistente: tasks `[ ]` com status built, "8 testes novos" quando são 9, "última gravação bem-sucedida" no Design Notes. Corrigir junto com o patch do `lastState`.
