# Deferred work

## Deferred from: code review of story-separar-main-go-em-arquivos-do-package-main-plan (2026-10-29)
- `proxy.go:40`: `proxyV1` devolve `reqErr.Error()` cru ao cliente em erro de transporte; vaza a URL do upstream e ignora `writeAPIError`. Tratar na story de failover.
- `config.go:223,262`: `MAX_REQUEST_BODY_BYTES` e `SMTP_PORT` inválidos são ignorados em silêncio, ao contrário das durações, que o `validateConfig` rejeita. Tratar na story de config dos tiers.
- `keys.go:105`, `server.go:226`: `KeyManager.AdvanceOnExhaustion` e `(*App).validateConfigAndPrint` não têm chamadores. Remover quando uma story puder mudar código.
- `config.go:96`: `yamlConfig` não tem `usage_url` nem `usage_cache_ttl`; só por variável de ambiente.
- `proxy.go:24`: detecção de body grande por `strings.Contains` no texto do erro; usar `errors.As` com `*http.MaxBytesError`.
