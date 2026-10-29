---
title: "Log estruturado por request"
type: 'feature'
ticket: 3
created: '2026-10-29'
status: done
baseline_revision: 'ff46ba12ab3f4c66d8950527fa5d414b5dff8213'
route: 'full'
route_source: 'auto'
risk: 'low'
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

**Problem:** O proxy não registra o que cada request fez: só há logs de partida e de falhas pontuais, então o Eduardo não vê qual modelo, tier ou chave atendeu, com que status e em quanto tempo.

**Approach:** `proxyV1` grava uma linha JSON (`log/slog`, handler JSON em stderr) por request, quando a resposta termina (inclusive o fim do stream), com `model`, `api_style`, `tier`, `key_index`/`key_name`, `status` e `latency_ms`. Um 429 local e um 502 sem tier saem com `tier` vazio; nenhuma linha traz o valor de uma chave.

## Boundaries & Constraints

**Always:** latência medida de antes de ler o corpo até o último byte copiado ao cliente; `status` é o que o cliente recebeu (o primeiro `WriteHeader`; 200 se só `Write`); tier 0 usa o nome fixo `opencode-go` com `key_index` e `key_name` (`a.config.keyName(i)`, pode ser vazio); tier extra usa `TierConfig.Name` e deixa `key_index`/`key_name` ausentes; `api_style` é `openai` ou `anthropic`; `model` é o `model` de topo do corpo, vazio se não houver; o `ResponseWriter` embrulhado continua implementando `http.Flusher` (streaming não pode travar); comportamento HTTP, headers e corpos das respostas não mudam.

**Never:** logar a chave (valor), `Authorization`, `x-api-key`, corpo ou headers da request; cooldown, persistência, Telegram, failover por conexão ou por 5xx; seção de tiers na página de status; mexer em `config.go`; flag ou config nova para o log; trocar o `log` padrão dos demais logs.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Tier 0 atende | `model: "glm-5.1"`, estilo OpenAI, chave índice 0 com nome | uma linha com `model`, `api_style:"openai"`, `tier:"opencode-go"`, `key_index:0`, `key_name`, `status:200`, `latency_ms` | No error expected |
| Tier 1 atende | tier 0 com 429 de cota em todas as chaves, modelo mapeado no tier 1 | uma linha com `tier` = nome do tier 1, sem `key_index` nem `key_name`, `status` do tier | No error expected |
| Stream | upstream manda eventos SSE com atraso entre eles | `latency_ms` >= tempo total do stream, não só até o primeiro byte | No error expected |
| 429 local | todas as chaves em cooldown, nenhum tier atende | `tier:""`, `status:429` | 429 local |
| 502 sem tier | erro de conexão ao upstream do tier 0 | `tier:""`, `status:502` | 502 |
| Sem segredo | chaves `secret-key-1` e `secret-tier-key` em uso | nenhuma linha contém esses valores | No error expected |
| Estilo Anthropic | request em `/v1/messages` | `api_style:"anthropic"` | No error expected |

</intent-contract>

## Code Map

- `proxy.go` -- `proxyV1` (lê o corpo, loop de chaves do tier 0, `proxyTiers`, 429 local) e `copyResponse` (copia e dá `Flush` em SSE; precisa do `http.Flusher` do writer). `proxyTiers` hoje devolve `bool`; ele precisa devolver também o nome do tier que respondeu (vazio no 502 de conexão). O `model` de topo é lido por `json.Unmarshal` em `proxyTiers`; extrair um helper compartilhado. Há um único chamador de `proxyTiers` (`proxyV1`).
- `server.go` -- `App` e `newApp`: acrescentar o campo `logger *slog.Logger` (padrão `slog.New(slog.NewJSONHandler(os.Stderr, nil))`). `ServeHTTP` chama `proxyV1` só para `isProxyPath` já autenticado; não tocar.
- `adminkeys.go:45` -- `Config.keyName(i)` para o nome da chave.
- `main_test.go` -- padrão: `httptest.NewServer` + `newApp(Config{...})` + `doProxyReq(app)` (linha 539); testes de tier em `1486-1654` (exemplos de config com `Tiers`, `TierModel`, `APIStyle`). Ali, `app.logger = slog.New(slog.NewJSONHandler(&buf, nil))` com buffer protegido por mutex captura as linhas.
- `docs/configuration.md` -- seção `## Tiers`; acrescentar uma seção curta `## Request log` com os campos.

## Tasks & Acceptance

**Execution:**
- [ ] `proxy.go` -- trocar `proxyV1` por um wrapper que embrulha o `ResponseWriter` num recorder de status (com `Flush`, `Unwrap`), mede o tempo e grava a linha no fim; mover o corpo atual para uma função interna que devolve `model`, `tier`, `key_index`, para o wrapper logar; `proxyTiers` passa a devolver o nome do tier que respondeu (vazio quando devolveu 502); extrair o helper do `model` de topo -- log por request
- [ ] `server.go` -- campo `logger` em `App` e valor padrão em `newApp` -- ponto único de saída do log, substituível em teste
- [ ] `main_test.go` -- testes da matriz: tier 0 e tier 1 conferindo os campos (inclusive `key_name`), stream com latência até o fim, 429 local, 502 sem tier, ausência dos valores de chave em todas as linhas, estilo Anthropic; confirmar que os testes existentes seguem passando -- cobrir a matriz
- [ ] `docs/configuration.md` -- seção `## Request log` com a linha de exemplo, os campos e o aviso de que a chave nunca é logada -- documentação

**Acceptance Criteria:**
- Given uma request atendida pelo tier 0 e outra pelo tier 1, when as duas terminam, then há duas linhas JSON com modelo, estilo, tier (e chave no tier 0), status e latência, e nenhuma contém o valor de uma chave.
- Given uma resposta em stream com atraso entre eventos, when o stream termina, then `latency_ms` cobre o stream inteiro.
- Given nenhum tier atendendo, when o cliente recebe o 429 local, then a linha tem `tier` vazio e `status` 429.

## Implementation Notes

- `proxyV1` virou wrapper com `statusRecorder` (`Flush`, `Unwrap`) e grava a linha `slog` JSON; o corpo antigo foi para `serveProxy`, que devolve `model`, `tier` e `keyIdx` (-1 fora do tier 0). `proxyTiers` recebe `fields`/`model` e devolve `(nome, handled)`; `topLevelModel` é o helper compartilhado. `App.logger` em `server.go`; seção `## Request log` em `docs/configuration.md`.
- Erros de corpo (413/400) também são logados, com `model` e `tier` vazios (decisão do executor).

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 5 findings — high 0, medium 2, low 2, false 0, maybe-false 0 (1 rejeitado como low)
- findings:
  - `[medium]` `patch` `MaxBytesReader` sobre o `statusRecorder` esconde `requestTooLarge()` e a conexão deixa de fechar após o 413 — `MaxBytesReader` agora usa o writer original; teste do 413 acrescentado
  - `[medium]` `patch` tier extra chamado `opencode-go` saía com `key_index`/`key_name` — `serveProxy` devolve `keyIdx` -1 fora do tier 0 e o log só põe chave com `keyIdx >= 0`; teste acrescentado
  - `[low]` `reject` `topLevelModel` interpreta o corpo de toda request — o log precisa do `model`, então o parse é inerente; a alternativa só adiciona um segundo caminho de parse
  - `[low]` `patch` 502 de erro de conexão do tier sem teste — caso acrescentado
  - `[low]` `patch` teste Anthropic sem checar status, tier e `key_index` — asserções acrescentadas
  - `[low]` `reject` status de tier não-200 só coberto em 200 — o recorder guarda o primeiro status de qualquer resposta; o 429 e o 502 já cobrem status diferentes de 200

## Auto Run Result

- Mudança: cada request proxiada grava uma linha JSON em stderr no fim da resposta (inclusive stream) com `model`, `api_style`, `tier`, `key_index`/`key_name` (só tier 0), `status` e `latency_ms`; 429 local e 502 sem tier saem com `tier` vazio; sem valor de chave.
- Arquivos: `proxy.go` (wrapper, `statusRecorder`, `serveProxy`, `topLevelModel`), `server.go` (`logger`), `main_test.go` (testes do log e do 413), `docs/configuration.md` (`## Request log`).
- Review: 6 achados; 4 patches aplicados (2 medium, 2 low), 0 adiados, 2 rejeitados (parse do corpo em toda request, inerente ao log; status de tier só em 200, já coberto por 429 e 502).
- Follow-up recomendado: sim, porque 2 patches `medium` foram aplicados sem nova revisão; o risco é a mudança de onde `MaxBytesReader` é montado e o novo discriminador `keyIdx`.
- Verificação: `go build ./...`, `go vet ./...`, `go test -race -count=1 ./...` passam; streaming coberto só por teste `httptest`, binário real não rodado.
- Risco residual: o log vai para stderr em formato JSON junto de logs de texto; o corpo é interpretado em toda request.

## Design Notes

`copyResponse` escreve o status uma vez com `WriteHeader` e depois dá `Flush` em SSE, então o recorder só precisa guardar o primeiro status e repassar `Flush`; o tempo termina quando a função interna retorna, ou seja, depois do último byte. Linha de exemplo:

```json
{"time":"...","level":"INFO","msg":"request","model":"glm-5.1","api_style":"openai","tier":"opencode-go","key_index":0,"key_name":"main","status":200,"latency_ms":412}
```

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test -race ./...` -- expected: todos passam, incluindo os testes novos

## Code Review

### 2026-10-29
Code review: 0 decision-needed, 4 patch, 0 defer, 17 rejected. Lenses: blind-hunter, edge-case-hunter, verification-gap, intent-alignment.

- [x] [Review][Patch] Teste do 413 não fixa `MaxBytesReader` no writer original (recorder não tem `requestTooLarge`); usar `httptest.NewServer` e afirmar `resp.Close` [main_test.go:TestRequestBodyTooLargeStill413]
- [x] [Review][Patch] Nenhum teste afirma `key_index:0` e `key_name` na linha de log (trocar `>= 0` por `> 0` passa); servir uma request pela chave 0 com nome [main_test.go:TestRequestLogTier0AndTier1, proxy.go:68]
- [x] [Review][Patch] `docs/configuration.md` `## Request log` não cita as linhas 413/400, que saem com `model` e `tier` vazios [docs/configuration.md:121]
- [x] [Review][Patch] `model` é logado sem limite de tamanho (cliente autenticado pode inflar a linha até o limite do corpo); truncar [proxy.go:67]

#### Rejected
- Stream truncado/abortado e panic: status 200 é o que o cliente recebeu; corrigir exige mudar a assinatura de `copyResponse` (baixo, complexo).
- Cancelamento do cliente logado como 502: raro, exige ramo extra (baixo).
- Failover invisível e 502 sem tier/chave: é o contrato do ticket (502 sem tier com `tier` vazio).
- `App.logger` sem nil guard: só `newApp` constrói `App`.
- Log em stderr misturado com `log` de texto: sem correção simples, já listado como risco residual.
- Inconsistências de metadados do plan: artefato do plan, não código.
- Testes faltando (chave sem nome, caminho 400, 429 local vs upstream): raros; limiar de 200ms não é flaky, pois o sleep garante >= 200ms.
- 1xx no `statusRecorder` (2 achados): o cliente Go não expõe 1xx em `resp.StatusCode`.
- Sem request ID: não pedido.
- `statusRecorder` sempre anuncia `Flusher`: writers do net/http implementam `Flusher`.
- Índice de log vazio nos testes: só falha em teste já quebrado.
- Dependência do fixture `tierDead`: especulativo, não verificado.
