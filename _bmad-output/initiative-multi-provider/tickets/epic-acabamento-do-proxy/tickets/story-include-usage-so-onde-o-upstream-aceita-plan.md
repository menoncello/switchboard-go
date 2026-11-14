---
title: 'include_usage só onde o upstream aceita'
type: 'bugfix'
ticket: '4'
created: '2026-11-14'
status: 'done'
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

## Code Review

### 2026-11-14

0 decision-needed, 3 patch, 1 defer, 12 rejeitados.

- [x] [Review][Patch] Falta teste negativo para `/v1/completions` em stream de tier pago; trocar a igualdade por `!= "/v1/responses"` passaria nos testes atuais [history_test.go:841]
- [x] [Review][Patch] `usageFields` não lê `input_tokens_details.cached_tokens` da Responses API, então `tokens_cache_in` fica 0 em `/v1/responses`; os testes novos têm o campo mas não checam `cache` [usage.go:27]
- [x] [Review][Patch] Comentário de `withIncludeUsage` e docs dizem que `/v1/responses` rejeita o campo e que só `/chat/completions` aceita; a evidência mostra que depende do modelo (`grok-4.7` aceita) [proxy.go:313]
- [x] [Review][Defer] `response.completed` acima de 1 MiB é descartado pelo `sseUsageScanner` e o stream cai em "Sem usage" com custo de pior caso [usage.go:77] — deferred: limite por linha anterior a esta story

Rejeitados:
- `response` não-objeto quebra `parseUsage`: false, nenhum formato que passa por `isProxyPath` tem esse campo no topo.
- `/v1/completions` perde `include_usage` e vai a pior caso: real, mas a intenção congelada e o R4 pedem só `/chat/completions`; mudar isso é renegociar a intenção.
- Mesma regressão em `/v1/completions` não reconhecida (claim): mesmo motivo do anterior.
- Condição inline sem helper: low, um só uso.
- Testes de `/responses` sem prefixo, tier gratuito, Anthropic, `response.incomplete`: false, `/responses` não é aceito por `isProxyPath`, os outros casos já estavam cobertos e `parseUsage` lê `response.usage` de qualquer evento.
- Intenção congelada não cita `parseUsage`: false, a mudança está nas Implementation Notes.
- "4 achados" com 5 itens: false, o quinto é nota do executor.
- Referências de linha frágeis e evidência curta no plano: low, registro histórico.
- `ticket: '4'` contra "5.4": false, é a convenção do ticket store.
- Barra final em `/v1/chat/completions/`: low, SDKs não mandam e `apiStyleForRequest` também não trata.
- `include_usage` enviado pelo cliente em `/v1/responses` sem teste: false, o proxy repassa o corpo, como esperado.
- Regra por caminho em vez de por modelo; sem chamada real pelo proxy depois da mudança: false, a intenção escolhe a regra por caminho e o verify pede a chamada que prova a rejeição (feita) e o go test.
