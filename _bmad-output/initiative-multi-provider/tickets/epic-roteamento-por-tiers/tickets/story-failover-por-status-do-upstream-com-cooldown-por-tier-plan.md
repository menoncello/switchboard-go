---
title: "Failover por status do upstream, com cooldown por tier"
type: 'feature'
ticket: 4
created: '2026-10-29'
status: built
baseline_revision: 'c3ac27d8b194518373288a501bab18723a1adbf0'
route: 'full'
route_source: 'auto'
risk: 'high'
review: 'quick'
review_source: 'pinned'
lenses_ran: [quick]
review_loop_iteration: 0
followup_review_recommended: true
context: []
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** Só o 429 de cota (e o 403 de assinatura no tier 0) faz failover: 401, 402, 404, 5xx e 429 comum chegam ao cliente, e nenhum tier fica em cooldown, então um provedor quebrado é chamado de novo a cada request.

**Approach:** Um cooldown em memória por tier (tier 0 incluso) e um laço de tiers que, em 401, 402, 404 elegível, 5xx ou 429, marca o tier, guarda a resposta e passa ao próximo; sem tier que atenda, o cliente recebe a última resposta do upstream, ou o 429 local com `Retry-After` quando todos estão em cooldown ou pulados.

## Boundaries & Constraints

**Always:** só `429` de cota e `403` de assinatura giram a chave no tier 0; os demais statuses de falha põem o tier 0 inteiro em cooldown. Tier em cooldown é pulado (tier 0 inclusive). Cooldown do tier extra = campo `cooldown` do tier (duração, `time.ParseDuration`, >= 0) ou, ausente, `RetryExhaustedAfter` (valor efetivo da config, resolvido na hora de usar); o do tier 0 = `RetryExhaustedAfter`; 0 = sem cooldown (failover só nesta request). 404 só falha o tier quando o corpo tem `model` de topo não vazio (no tier extra ele é sempre mapeado) e o corpo do 404 não indica modelo inexistente (contém "model" e um de "not found", "does not exist", "unknown", "invalid", "no such"); senão o 404 segue ao cliente sem cooldown. Qualquer outro status (400, 403 comum, 2xx, 3xx, 4xx) segue ao cliente sem cooldown. Failover só antes de copiar a resposta; depois do primeiro byte não há troca. A resposta de falha retida mantém corpo legível até o fim (feche a anterior ao reter outra e a retida ao servir ou ao cair no 429). O log por request (ticket 3) continua: `tier` e `key_index` da resposta servida, incluindo a última resposta de falha.

**Never:** erro de conexão ou timeout com failover (continua 502 imediato; é a story 12); persistência em arquivo, Telegram, seção de tiers na página de status, `min_context`/`tools`; traduzir estilos; mudar URL, `PROXY_API_KEY` ou a config de chaves; logar valor de chave.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Status de falha no tier 0 | tier 0 responde 401, 402, 404 (modelo mapeado), 500 ou 429 comum; tier 1 atende | tier 0 em cooldown, tier 1 atende; a request seguinte vai direto ao tier 1 sem tocar o tier 0 | No error expected |
| Falha no tier extra | tier 1 responde 401, 402, 404, 5xx ou 429 e o tier 2 atende | tier 1 em cooldown, tier 2 atende | No error expected |
| 400 | qualquer tier responde 400 | 400 vai ao cliente, sem cooldown | repassado |
| 404 de modelo inexistente | corpo do 404 diz que o modelo não existe | 404 vai ao cliente, sem cooldown | repassado |
| Stream iniciado | tier responde 200 SSE e o corpo é cortado | cliente fica com esse stream, sem troca de tier | No error expected |
| Cooldown expira | tier em cooldown, relógio passa do prazo | tier volta a ser tentado | No error expected |
| Todos falham | tier 0 dá 500 e tier 1 dá 503 | cliente recebe o 503 do tier 1 (última resposta), cooldown nos dois | repassado |
| Todos em cooldown ou pulados | tier 0 e tier 1 em cooldown | 429 local com `Retry-After` = menor tempo restante entre os tiers elegíveis (tier 0 com cooldown de tier ou, sem ele, com chaves esgotadas) | 429 local |
| Cota no tier 0 | 429 de cota ou 403 de assinatura | gira a chave como hoje; só com todas as chaves esgotadas o tier segue ao próximo | No error expected |

</intent-contract>

## Code Map

- `proxy.go` -- `serveProxy` (laço de chaves do tier 0, `proxyTiers`, 429 local) e `proxyTiers`. Hoje 4xx/5xx comuns são copiados e só `isQuota429` pula o tier extra. Reaproveitar `isQuota429`/`isKeyDead` (restauram o corpo), `tierModelID`, `copyResponse`, `topLevelModel`. Não mudar `doUpstream`, nem o 502 de conexão, nem o wrapper de log (`proxyV1`).
- `config.go` -- `TierConfig` e `yamlTier`/`convertTiers`: acrescentar `Cooldown *time.Duration` (yaml `cooldown`, nil = padrão); rejeitar duração inválida ou negativa (nos mesmos moldes de `retry_exhausted_after`).
- `server.go` -- `App`/`newApp`: acrescentar o estado de cooldown por nome de tier (mapa `nome -> até`, com mutex e `now` injetável como em `KeyManager`); `tier0Name` é a chave do tier 0. Novo arquivo no `package main` é aceitável.
- `keys.go` -- `KeyManager.RetryAfterSeconds`, `ShouldNotifyAllExhausted`, `AllExhausted`: reaproveitar para o `Retry-After` e para o alerta SMTP, que só deve disparar quando as chaves do tier 0 estão todas esgotadas.
- `main_test.go` -- fixtures `newTierFixture`/`tierReq` (~1440-1490) e testes de tier (~1487-1650); `TestTierNonQuotaResponseCopiedWithoutFurtherFailover` e `TestTierNonQuota429CopiedWithoutFurtherFailover` documentam o comportamento antigo e devem mudar; `TestProxyPassesThroughOther403` e testes de chave (~548-1000) devem seguir passando, salvo onde 5xx/429 comum no tier 0 mudou.
- `docs/configuration.md` -- seção `## Tiers`: reescrever o parágrafo de failover.

## Tasks & Acceptance

**Execution:**
- [ ] `config.go` -- campo `cooldown` por tier (parse e validação) -- cooldown por tier
- [ ] `server.go` (ou `cooldown.go`) -- estado de cooldown por tier, com `now` injetável e consulta do tempo restante -- base do pulo e do `Retry-After`
- [ ] `proxy.go` -- classificador de status de falha (`401`, `402`, `404` elegível, `>=500`, `429`), tier 0 pulado em cooldown e posto em cooldown nos statuses que não giram chave, laço de tiers com pulo/cooldown/retenção da última resposta, `Retry-After` calculado entre os tiers elegíveis -- failover por status
- [ ] `main_test.go` -- testes da matriz (cada status com um upstream que falha e outro que atende, 400, 404 de modelo inexistente, stream iniciado, volta após o cooldown com relógio injetado, última resposta quando todos falham, 429 local com `Retry-After`, `cooldown` do tier e padrão); atualizar os dois testes antigos; `go test -race ./...` passa -- cobrir a matriz
- [ ] `docs/configuration.md` -- `cooldown` na tabela de campos, regras de failover e cooldown, 404 de modelo inexistente, última resposta e 429 local; remover as frases antigas ("any other response... without further failover", "só quota 429") -- documentação

**Acceptance Criteria:**
- Given tier 0 responde cada um de 401, 402, 404 (modelo mapeado), 500 e 429 comum e o tier 1 atende, when a request chega, then o tier 1 responde, o tier 0 fica em cooldown e a request seguinte não chama o tier 0.
- Given um tier em cooldown e o relógio além do prazo, when chega request, then o tier é tentado de novo.
- Given 400 ou 404 de modelo inexistente, when o tier responde, then o cliente recebe a resposta sem cooldown.
- Given todos os tiers falham com 5xx, when termina, then o cliente recebe a resposta do último tier tentado; given todos em cooldown, then 429 local com `Retry-After` do primeiro a voltar.
- Given uma resposta de stream já iniciada, when ela é cortada, then nenhum outro tier é chamado.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 6 findings — high 0, medium 2, low 3, false 0, maybe-false 1 (1 rejeitado)
- findings:
  - `[low]` `patch` `docs/configuration.md` dizia "Without `tiers:` the proxy behaves as before", falso com o cooldown do tier 0 — frase reescrita
  - `[medium]` `patch` 404 (e 429 comum retido) servido ao cliente truncado em 4096 bytes com `Content-Length` original — `peekBody` devolve o resto do corpo; teste com corpo > 4 KiB
  - `[low]` `patch` tier extra chamado `opencode-go` compartilhava o cooldown do tier 0 — tier 0 passou a usar a chave `tier0CooldownKey = ""`; teste de independência
  - `[low]` `reject` `MarkAvailable` roda antes de `isTierFailure` e rearma notificações numa falha — comportamento anterior ao ticket (qualquer resposta não "chave morta" já marcava a chave), fora do que a story muda
  - `[medium]` `patch` alerta SMTP "all exhausted" deixava de disparar quando as chaves do tier 0 estão esgotadas e os extras falham com 5xx — alerta movido para antes de servir a última resposta; teste
  - `[maybe-false]` `patch` lacunas de teste (alerta, `Retry-After` pelas chaves, colisão de nome, corpo grande) — testes acrescentados para cada uma

## Auto Run Result

- Mudança: cooldown em memória por tier (tier 0 incluso, campo `cooldown` por tier com padrão `retry_exhausted_after`); 401, 402, 404 elegível, 5xx e 429 põem o tier em cooldown e a request segue ao próximo; 400 e 404 de modelo inexistente voltam ao cliente; sem tier que atenda, o cliente recebe a última resposta do upstream, ou o 429 local com `Retry-After` do tier que volta primeiro.
- Arquivos: `cooldown.go` (estado), `config.go` (`cooldown`), `server.go` (`App.cooldowns`), `proxy.go` (classificador, laço de tiers, `peekBody`, `localRetryAfter`), `main_test.go`, `docs/configuration.md`.
- Review: 6 achados; 4 patches aplicados (2 medium, 2 low), mais testes de lacuna; 0 adiados; 1 rejeitado (`MarkAvailable` antes do status de falha: comportamento anterior ao ticket).
- Follow-up recomendado: sim, porque 2 patches `medium` (corpo truncado, alerta SMTP) foram aplicados sem nova revisão.
- Verificação: `go build ./...`, `go vet ./...`, `go test -race -count=1 ./...` passam; sem rodar contra upstream real.
- Risco residual: sem `tiers:`, uma falha do tier 0 dá o 429 local até o cooldown acabar; cooldown não persiste a reinício (story seguinte).

## Design Notes

Retenção da última resposta: `last *http.Response` com `lastTier`/`lastKey`; ao reter outra, `last.Body.Close()`. Fim do laço: `last != nil` -> `copyResponse` e devolve `(model, lastTier, lastKey)`; senão o 429 local. Tier 0 em cooldown de tier não conta as chaves: `Retry-After` do tier 0 = o restante do cooldown de tier; sem cooldown de tier e com chaves esgotadas = `keys.RetryAfterSeconds`. Sem tempo conhecido, sem header.

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test -race -count=1 ./...` -- expected: todos passam, incluindo os novos
