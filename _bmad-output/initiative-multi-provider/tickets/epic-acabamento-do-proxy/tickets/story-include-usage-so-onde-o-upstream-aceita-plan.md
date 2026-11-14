---
title: 'include_usage só onde o upstream aceita'
type: 'bugfix'
ticket: '4'
created: '2026-11-14'
status: 'built'
baseline_revision: '5713ac505e46ab45c5404d9000fc568c299bf179'
route: 'oneshot'
route_source: 'auto'
risk: 'medium'
review: 'quick'
review_source: 'pinned'
lenses_ran: ['quick']
review_loop_iteration: 0
context: []
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Em tier pago, `proxyTiers` injeta `stream_options.include_usage: true` em todo request OpenAI com `stream: true`, inclusive `/v1/responses`. O OpenCode Go rejeita o campo nesse caminho para modelos GPT (`gpt-5.6-luna` → 400 `unknown_parameter`), e 400 não faz failover, então a request quebra.

**Approach:** `withIncludeUsage` passa a ser aplicado só quando o caminho é `/chat/completions` (com ou sem o prefixo `/v1`); `/v1/responses` e demais caminhos vão com o corpo inalterado. A Responses API já manda `usage` no `response.completed`. `docs/configuration.md` passa a dizer que a injeção vale só para `/chat/completions`. Um teste prova que `/chat/completions` recebe o campo e `/v1/responses` não.

</frozen-after-approval>

## Implementation Notes

Oneshot: mudança de uma condição em `proxy.go:366`, um teste em `history_test.go` (junto de `TestPaidTierStreamIncludeUsage`) e uma frase em `docs/configuration.md:276-278`.

Evidência do passo hitl (2026-11-14), chamadas reais ao tier 0 (`https://opencode.ai/zen/go/v1/responses`) da VPS, `stream: true`, com o cabeçalho `x-opencode-session` (sem ele o upstream devolve 400 `MissingSessionID` em qualquer caso):
- `grok-4.7` + `include_usage` → 200, `usage` no `response.completed`.
- `gpt-5.6-luna` + `include_usage` → 400 `Unknown parameter: 'stream_options.include_usage'`.

Arquivos: `proxy.go` (condição em `proxyTiers` e comentário de `withIncludeUsage`), `usage.go` (`parseUsage` lê `response.usage`), `history_test.go` (testes de `/v1/responses` sem o campo, de `/chat/completions` sem prefixo e de usage do `response.completed`), `docs/configuration.md`. O teste novo de `/v1/responses` falha sem a correção em `proxy.go` (checado revertendo a condição).

## Review Triage Log

Passada quick, 4 achados:
- medium, patch: `parseUsage` não lia `response.usage`, então streams de `/v1/responses` em tier pago caíam em "Sem usage" com custo de pior caso. O bug já existia antes, porque `include_usage` não muda o formato do evento (as duas chamadas reais trouxeram `usage` dentro de `response`). Foi corrigido porque a premissa da story depende disso. A correção ganhou teste unitário e ponta a ponta.
- low, patch: o bullet `usage_missing` em `docs/configuration.md` estava desatualizado e foi corrigido.
- low, patch: faltava teste para `/chat/completions` sem o prefixo `/v1` (`isProxyPath` aceita esse caminho). Teste adicionado.
- low, rejeitado: não há teste para outros caminhos OpenAI (`/v1/completions`, `/v1/embeddings`). A condição de igualdade já exclui esses caminhos, e o teste de `/v1/responses` cobre o lado negativo.
- Nota do executor, rejeitada como low: um JSON com `"response"` que não seja objeto faria o `parseUsage` falhar. Nenhum formato que o proxy trata (OpenAI e Anthropic) tem esse campo no topo.
