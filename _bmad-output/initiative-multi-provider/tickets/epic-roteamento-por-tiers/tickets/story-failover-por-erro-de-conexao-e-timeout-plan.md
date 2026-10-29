---
title: "Failover por erro de conexão e timeout"
type: 'feature'
ticket: 12
created: '2026-10-29'
status: done
baseline_revision: 'f080aef9ed7681432de2f599855d3d407d56ec5a'
route: 'full'
route_source: 'auto'
risk: 'medium'
review: 'quick'
review_source: 'pinned'
lenses_ran: [quick]
review_loop_iteration: 0
followup_review_recommended: false
context: []
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** Erro de conexão e timeout antes do primeiro byte (inclusive o `response_header_timeout`) viram `502` imediato no tier 0 e em qualquer tier extra; a request não passa ao próximo tier mesmo havendo um saudável.

**Approach:** `doUpstream` com erro (e o contexto do cliente ainda vivo) conta como falha da tentativa: a request segue ao próximo tier sem cooldown e sem girar chave; se nenhum tier atende, vale a resposta de falha retida, senão `502`. Contexto do cliente cancelado encerra a request sem tocar estado nenhum.

## Boundaries & Constraints

**Always:** erro de conexão ou timeout não põe o tier em cooldown (nem o tier 0), não marca chave esgotada e não gira chave: no tier 0 sai do laço de chaves e vai aos tiers extras. Request sem `model` de topo continua só no tier 0 e, com erro, devolve `502`. Sem tier que atenda: a resposta de falha retida (status de upstream) vai ao cliente como na story 4; sem resposta retida e com erro de conexão, `502` com corpo que não vaza a URL do tier (o do tier 0 mantém o texto atual); sem erro e sem resposta, o 429 local de sempre. Cliente que cancela (`r.Context().Err() != nil` depois do erro) encerra a request: sem failover, sem cooldown, sem alerta SMTP, sem chamar outro tier; o log grava `status` 499 e `tier` vazio.

**Never:** failover depois do primeiro byte; cooldown por erro de conexão; mudar `doUpstream`, `isTierFailure`, `isKeyDead`, a config ou o formato de log; persistência, Telegram, estilos por tier, `min_context`.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Conexão recusada no tier 0 | tier 0 sem servidor; tier 1 atende | tier 1 atende; tier 0 sem cooldown e chaves intactas; a request seguinte tenta o tier 0 de novo | No error expected |
| Timeout no tier 0 | tier 0 não manda cabeçalho antes do `response_header_timeout`; tier 1 atende | tier 1 atende, sem cooldown | No error expected |
| Falha de conexão num tier extra | tier 1 recusa a conexão, tier 2 atende | tier 2 atende; tier 1 sem cooldown | No error expected |
| Todos inalcançáveis | tier 0 e tier 1 recusam a conexão | `502`, log `tier` vazio, nenhum cooldown | `502` |
| Resposta retida e depois erro | tier 0 dá 500 (em cooldown), tier 1 recusa a conexão, tier 2 atende | tier 2 atende | No error expected |
| Resposta retida e fim dos tiers | tier 0 dá 500, tier 1 recusa a conexão, sem outro tier | cliente recebe o 500 do tier 0 | repassado |
| Sem model | `GET /v1/models` com tier 0 inalcançável | `502`, tiers extras não chamados | `502` |
| Cliente cancela | contexto da request cancelado com o tier lento | nenhum outro tier chamado, nenhum cooldown, log 499 | encerrada |

</intent-contract>

## Code Map

- `proxy.go` -- `serveProxy` (linhas ~108-159): hoje `reqErr != nil` no laço do tier 0 faz `http.Error(502)` e `return`; trocar por `break` (sem `MarkExhausted`/`MarkAvailable`) guardando o erro. `proxyTiers` (~233-266): hoje `err != nil` do `doUpstream` devolve `("", true)` com 502; trocar por `continue` guardando o erro. Cancelamento: depois de qualquer `doUpstream` com erro, `r.Context().Err() != nil` -> `w.WriteHeader(499)` e retorno sem tier. Guardar o erro em `heldResponse` (campo `err`) ou variável local; fim de `serveProxy`: `last.resp != nil` serve a retida; senão `err != nil` -> `502`; senão 429 local. O alerta `ShouldNotifyAllExhausted` fica como está (só dispara com as chaves esgotadas), mas não no cancelamento.
- `proxy.go` -- reaproveitar `heldResponse`, `copyResponse`, `tierModelID`, `localRetryAfter`; não mudar `doUpstream` nem o wrapper `proxyV1`.
- `cooldown.go`, `config.go` -- sem mudança.
- `main_test.go` -- `newTierFixture`/`tierReq` (~1440-1490), `failoverApp`/`newStub`/`chatReq` (~2060-2130). Reescrever `TestTierConnectionErrorReturns502` (~1631) e `TestFailoverHeldTier0FailureThenUnreachableTierReturns502` (~2120), que descrevem o 502 antigo; `TestRequestLogLocal429And502HaveNoTier` (~1769) deve seguir passando (um tier único inalcançável continua 502).
- `docs/configuration.md` -- linha ~120 ("A connection error or timeout to a tier returns `502` (no failover)") e o log (~158).

## Tasks & Acceptance

**Execution:**
- [ ] `proxy.go` -- erro de `doUpstream` no tier 0 e nos extras vira falha sem cooldown (segue ao próximo tier), 502 só sem resposta retida, cancelamento do cliente encerra com 499 -- failover por conexão e timeout
- [ ] `main_test.go` -- testes da matriz: conexão recusada e `response_header_timeout` curto no tier 0 e num extra (sem cooldown, a request seguinte tenta o tier de novo), todos inalcançáveis, resposta retida seguida de erro, sem `model`, cliente que cancela (contexto cancelado, nenhum tier seguinte chamado, cooldown vazio, log 499); reescrever os dois testes antigos; `go test -race ./...` passa -- cobrir a matriz
- [ ] `docs/configuration.md` -- trocar a frase do 502 pela regra nova (failover sem cooldown, 502 só quando nada atende, cancelamento do cliente não conta); ajustar a descrição do log -- documentação

**Acceptance Criteria:**
- Given um upstream que recusa a conexão e outro que atende, when chega request com modelo mapeado, then o tier que atende responde e o tier que recusou continua sem cooldown.
- Given um upstream que estoura o `response_header_timeout` e outro que atende, when chega request, then o tier seguinte responde, sem cooldown.
- Given um cliente que cancela a request, when o tier atual falha pelo cancelamento, then nenhum outro tier é chamado e nenhum cooldown muda.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 6 findings — high 0, medium 0, low 5, false 1, maybe-false 0
- findings:
  - `[low]` `reject` timeout testado só no tier 0 — extra tier usa o mesmo ramo `err != nil` de `proxyTiers` que a conexão recusada, já testada; teste a mais não acha defeito
  - `[low]` `reject` primeira request sem `rec.Code` e sem hit zero no s0 — cooldown, corpo e `key_index == 0` já cobrem a regressão de rotação
  - `[low]` `reject` sem teste de "sem alerta SMTP no cancelamento" — o ramo 499 retorna antes de `ShouldNotifyAllExhausted`; fixar exige sender falso novo
  - `[false]` `reject` `err` antigo vira 499 num cancelamento posterior — o cancelamento só é visto com `orig.Err() != nil`, e o cliente que foi embora não recebe resposta de qualquer forma
  - `[low]` `reject` 502 do tier 0 mostra o erro cru do `client.Do` — texto do tier 0 mantido de propósito pelo plano (comportamento anterior ao ticket)
  - `[low]` `reject` quebra de linha na doc — cosmético

## Auto Run Result

- Mudança: erro de conexão e timeout antes do primeiro byte (tier 0 e extras) passam a request ao próximo tier sem cooldown e sem girar chave; sem tier que atenda vale a resposta retida, senão 502; cliente que cancela encerra com 499 sem tocar estado.
- Arquivos: `proxy.go` (`heldResponse.err`, laço do tier 0, `proxyTiers`, 499), `main_test.go` (testes novos e dois reescritos), `docs/configuration.md` (regra de failover e log).
- Review: 6 achados; 0 patches, 0 adiados, 6 rejeitados (5 low, 1 false) com motivo no log.
- Follow-up recomendado: não.
- Verificação: `go build ./...`, `go vet ./...`, `go test -race -count=1 ./...` passam; sem rodar contra upstream real.
- Risco residual: o 502 do tier 0 mantém o texto cru do erro (inclui a URL do tier 0); timeout num tier extra sem teste próprio.

## Design Notes

Distinguir cancelamento de falha olhando `r.Context().Err()` depois do erro, não o tipo do erro: o `response_header_timeout` também aparece como erro do `client.Do` e não deve ser confundido com cancelamento. Para testar o timeout, use um stub que dorme além de um `UpstreamResponseHeaderTimeout` curto.

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test -race -count=1 ./...` -- expected: todos passam, incluindo os novos

## Code Review

### 2026-10-29
0 `decision-needed`, 4 `patch`, 0 `defer`, 17 rejected.

- [x] [Review][Patch] Nenhum teste conta as tentativas no tier 0 (trocar `break` por `continue` passa); adicionar contador de hits e afirmar 1 tentativa por request com duas chaves [proxy.go:122]
- [x] [Review][Patch] Timeout sem teste num tier extra e sem passar pela config `response_header_timeout` (o teste ajusta o Transport à mão) [main_test.go]
- [x] [Review][Patch] 502 do tier 0 devolve o erro cru do `client.Do` (com a URL do tier); tiers extras devolvem texto fixo [proxy.go:163]
- [x] [Review][Patch] Cancelamento com resposta retida e sem `last.err` serve a resposta retida e loga o status dela, não 499 [proxy.go:150]

#### Rejected
- Latência sem limite, retry após timeout duplicar trabalho, timeout sem cooldown por request: falsos, são o comportamento pedido pelo ticket.
- 499 vaza body retido / `hold()` perde `err` / `orig` vs `r.Context()`: falsos (`defer last.release()`, `release` não zera a struct, `orig := r.Context()`).
- Erro não-rede em `doUpstream` sem cooldown: falso, só ocorre com URL malformada.
- Falha silenciosa no log, higiene de teste, linha longa na doc, asserção de SMTP no cancelamento, pontas soltas do plan: baixos, correção cria complexidade ou edita o plan.
