---
title: 'Aviso no Telegram quando todos os tiers falham'
type: 'feature'
ticket: 10
created: '2026-10-29'
status: 'done'
baseline_revision: '5ab66f8a517b4dd61ae220348053a7751a535bb5'
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

**Problem:** Quando nenhum tier atende uma request, só o SMTP avisa o Eduardo, e só se todas as chaves do tier 0 estiverem esgotadas. Falta o canal Telegram (R16), uma vez por episódio.

**Approach:** Bloco `telegram:` (`bot_token`, `chat_id`) no YAML. Um notificador novo envia `sendMessage` à API do Telegram na primeira falha de um episódio e só rearma depois que algum tier atende. Convive com o SMTP, que não muda.

## Boundaries & Constraints

**Always:** envio assíncrono, com timeout, sem afetar a request; falha de envio só loga, sem o token (o `*url.Error` carrega a URL com o token, então nunca logar o erro cru); token fora de `safeConfigSummary` e de qualquer log; sem `telegram:` completo (token e chat_id) nada é enviado; cancelamento do cliente não é falha.

**Never:** variáveis de ambiente para o Telegram; mexer no gatilho do SMTP; retry de envio; mensagem com chave, URL de tier ou corpo da request.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Primeira falha | nenhum tier atende, aviso armado | cliente recebe o erro de sempre; uma mensagem enviada | No error expected |
| Mesma falha de novo | segunda request sem tier no mesmo episódio | nada enviado | No error expected |
| Rearme | um tier atende, depois nova falha | nova mensagem enviada | No error expected |
| Sem config | sem `telegram:` ou só um dos dois campos | nada enviado | No error expected |
| Envio falha | servidor Telegram devolve 500 ou fora do ar | request do cliente intacta; log sem o token | Loga status ou erro genérico |

</intent-contract>

## Code Map

- `proxy.go` `serveProxy` -- linha ~158 "No tier served it" (ponto da falha, ao lado do SMTP); sucesso em ~147 (tier 0 serve) e ~151-152 (`proxyTiers` ok): rearmar ali
- `notify.go` -- `SMTPNotifier` e `sendMail`; modelo do padrão (`go n.send`, var substituível nos testes); não alterar
- `config.go` -- `Config` (campo novo), `yamlConfig` (+ `telegram`), `loadYAMLConfig` (copia), `mergeConfig` (campo a campo como SMTP), `safeConfigSummary` (~486: só `telegram_configured=%t`)
- `server.go` -- `App` e `newApp`: campo `telegram` e construção
- `docs/configuration.md` -- bloco YAML de exemplo (perto de `smtp:`, linha ~64) e a frase sobre o alerta SMTP (~154)
- `main_test.go` -- `failoverApp`, `newStub`, `chatReq` (~2505-2540) para montar o cenário; teste do SMTP `TestFailoverAllKeysExhaustedAndTiersFailStillAlerts` como modelo; `safeConfigSummary` testado ~1367

## Tasks & Acceptance

**Execution:**
- [ ] `telegram.go` -- `TelegramConfig{BotToken, ChatID}`, `TelegramNotifier` com estado armado/desarmado sob mutex; `notifyFailure(model)` (envia só se configurado e armado, desarma; `go` com timeout de ~10s e client próprio), `rearm()`; URL base em `var telegramAPIBase = "https://api.telegram.org"` para o teste; erro de envio logado sem token -- notificador isolado e testável
- [ ] `config.go` -- campo `Telegram TelegramConfig`; `yaml:"telegram"` com `bot_token` e `chat_id` (chat_id aceita número ou string: tipo `string` basta com YAML escalar); `mergeConfig`; `safeConfigSummary` com `telegram_configured` -- config só em YAML
- [ ] `server.go` e `proxy.go` -- `App.telegram` criado em `newApp`; `serveProxy` chama `notifyFailure` no ponto da falha (depois do `orig.Err()` do cancelamento, então cancelamento não conta) e `rearm()` quando algum tier atende
- [ ] `docs/configuration.md` -- documentar `telegram:` (campos, uma vez por episódio, convive com SMTP, token no arquivo como as demais credenciais)
- [ ] `main_test.go` -- servidor Telegram falso (`httptest`): primeira falha envia e o cliente recebe o erro; segunda no mesmo episódio não envia; depois de um tier atender, nova falha envia; sem `telegram:` nada enviado; falha de envio (500) não afeta a request; `safeConfigSummary` e logs sem o token

**Acceptance Criteria:**
- Given `telegram:` configurado e nenhum tier disponível, when chega uma request, then o cliente recebe o erro de sempre e o servidor Telegram falso recebe um `sendMessage` com o `chat_id` configurado.
- Given o aviso já enviado neste episódio, when outra request falha sem que nenhum tier tenha atendido, then nada é enviado.
- Given um tier atende uma request, when a próxima falha, then um novo aviso é enviado.
- Given a config sem `telegram:`, when nenhum tier atende, then nada é enviado e o SMTP se comporta como antes.
- Given o envio ao Telegram falha, when a request falha, then a resposta ao cliente não muda e o token não aparece em log nem em `safeConfigSummary`.

## Design Notes

Episódio = intervalo entre dois sucessos de qualquer tier. Resposta 400 devolvida pelo upstream conta como tier que atendeu (não há failover nela), então rearma. Falha por 502 de conexão e o 429 local também são falha. O estado vive no notificador (memória), não é persistido: um reinício começa armado.

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test -race -count=1 ./...` -- expected: todos passam, incluindo os novos

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 3 findings — high 0, medium 1, low 2, false 0, maybe-false 0
- findings:
  - `[medium]` `patch` modelo do cliente sem limite no texto do Telegram; um modelo >4096 caracteres faz o envio falhar e silencia o aviso no episódio — `truncateModel` (corte por runa em `maxLoggedModel`) compartilhado com o log; teste `TestTelegramLongModelIsBounded`
  - `[low]` `patch` buffer de log compartilhado entre subtestes, o caso "unreachable" passava pela linha do caso anterior — buffer por subteste e asserção na linha exata
  - `[low]` `reject` log de partida em `main.go` não mostra `telegram_configured` — cosmético; a linha também não mostra tiers e `safeConfigSummary` já cobre

## Auto Run Result

- Mudança: `telegram:` (`bot_token`, `chat_id`) no YAML; quando nenhum tier atende, o Eduardo recebe uma mensagem no Telegram uma vez por episódio, rearmada quando algum tier atende. Falha de envio só loga, sem o token. Convive com o SMTP.
- Arquivos: `telegram.go` (notificador), `config.go`, `server.go`, `proxy.go` (rearme, aviso e `truncateModel`), `docs/configuration.md`, `main_test.go`.
- Review: 3 achados; 2 patches aplicados (1 medium, 1 low), 1 rejeitado (motivo no log).
- Follow-up recomendado: não.
- Verificação: `go build`, `go vet` e `go test -race -count=1 ./...` passam, antes e depois dos patches.
- Risco residual: não testado contra a API real do Telegram (formato de erro); aviso perdido se o envio falhar, sem retry, como definido no plano.

## Code Review

### 2026-10-29 — 0 decision-needed, 6 patch, 0 defer, 15 rejected

- [ ] [Review][Patch] Teste: rearme quando o tier 0 atende após uma falha [proxy.go:153]
- [ ] [Review][Patch] Teste: cancelamento do cliente não envia aviso nem consome o episódio [proxy.go:159]
- [ ] [Review][Patch] Teste: `newApp` com `Telegram` na config envia sem sobrescrever `app.telegram` [server.go:46]
- [ ] [Review][Patch] Teste: `telegram_configured=false` com só um dos dois campos [config.go:499]
- [ ] [Review][Patch] Teste: resposta 4xx de upstream que não é falha de tier rearma; gatilho do SMTP segue igual com Telegram ligado [proxy.go:150]
- [ ] [Review][Patch] `bot_token` e `chat_id` sem `strings.TrimSpace` (`state_file` já é aparado) [config.go:268]

#### Rejected
- Aviso perdido se o envio falha (2 achados): o plano aceita sem retry; rearmar na falha dispararia um envio por request.
- Ponteiro nil em `App.telegram`: falso, nenhum `App{}` literal fora de `newApp`.
- Mutex a cada sucesso: custo desprezível.
- Telegram pela metade sem aviso: `telegram_configured=false` já aparece no resumo.
- Mensagem pobre, sem aviso de recuperação nem /status: fora do escopo.
- `truncateModel` com pânico: falso, o modelo vem de JSON decodificado (UTF-8 válido).
- Detalhes de `send` (client novo, globais nos testes): low sem impacto.
- Checkboxes do plano e dica de `chat_id` na doc: low.
- Episódio global rearmado por outro modelo: é o contrato ("algum tier atende").
- Token com `/`, `?` na URL: falso, tokens do Telegram são `[0-9]+:[A-Za-z0-9_-]+`.
- Espera fixa de 300ms em assertivas negativas: inerente a checar ausência.

### 2026-10-29 (2ª rodada) — 0 decision-needed, 5 patch, 0 defer, 14 rejected

- [x] [Review][Patch] Envio falho rearma o episódio: `send` devolve bool e `notifyFailure` chama `rearm()` quando falha (decidido: opção 2) [telegram.go:notifyFailure]
- [x] [Review][Patch] Teste: rearme quando o tier 0 atende após uma falha [proxy.go:153]
- [x] [Review][Patch] Teste: cancelamento do cliente não envia aviso nem consome o episódio [proxy.go:159]
- [x] [Review][Patch] Teste: `newApp` com `Telegram` na config liga `app.telegram` sem sobrescrever [server.go:46]
- [x] [Review][Patch] Docs: reinício começa armado, envio falho não é repetido, resposta 4xx do upstream também rearma [docs/configuration.md]

#### Rejected
- Ponteiro nil em `App.telegram`: falso, único `App{}` literal é `newApp` (server.go:46).
- Flapping/rearme por sucesso de outro modelo (2 achados): é o contrato do ticket; intervalo mínimo exige estado novo.
- Token/chat_id sem trim (2 achados): scalars YAML já vêm aparados, só aspas explícitas escapam; rara.
- Mensagem sem contexto, log global vs slog, startup sem `telegram_configured`: low/cosmético.
- Aviso perdido no shutdown: rara, exigiria WaitGroup.
- Espera de 300ms em assertivas negativas, teste -race concorrente, chat_id negativo: low.
