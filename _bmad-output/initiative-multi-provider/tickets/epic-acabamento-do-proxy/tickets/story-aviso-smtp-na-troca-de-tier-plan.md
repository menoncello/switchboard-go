---
title: 'Aviso SMTP na troca de tier'
type: 'feature'
ticket: '6'
created: '2026-11-14'
status: 'built'
baseline_revision: '0af9a051d84b30908ae0da7b23f19cba3ecc42c9'
route: 'oneshot'
route_source: 'auto'
risk: 'medium'
review: 'quick'
review_source: 'pinned'
lenses_ran: ['quick']
review_loop_iteration: 0
followup_review_recommended: false
context: []
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** Hoje o SMTP só avisa quando todas as chaves do tier 0 esgotam (`NotifyAllExhausted`) ou no primeiro uso diário de um tier pago. Quando uma request sai do tier 0 e é atendida por um tier extra, o Eduardo não fica sabendo.

**Approach:** Quando uma request que o tier 0 não atendeu é servida por um tier extra, o `SMTPNotifier` manda um aviso ("Switchboard Go switched tier: <tier>", com o modelo no corpo) pelo mesmo `send` do alerta de primeiro uso do tier pago. Vale uma vez por episódio, como o `TelegramNotifier`: o aviso desarma ao sair e rearma quando o tier 0 volta a atender uma request. Se o SMTP não está configurado, nada desarma. Se o envio falha, rearma para tentar na próxima troca.

</intent-contract>

## Implementation Notes

Oneshot: a mudança fica em torno de 30 linhas de código e 60 de teste.

- `notify.go`: `SMTPNotifier` ganha `mu sync.Mutex` + `switchDisarmed bool` e `NotifyTierSwitch(tier, model string)` com o mesmo padrão de `TelegramNotifier.notifyFailure` (checar configuração com Host/To/From, desarmar sob lock, `go` com envio e rearme se falhar), além de `rearmTierSwitch()`. `send` passa a devolver `bool` (`false` quando `sendMail` falha); quem chama hoje ignora o retorno. Use `truncateModel(model)` no texto.
- `proxy.go`, `serveProxy`: no `return` do tier 0 que atendeu (junto de `a.telegram.rearm()`), chamar `a.sender.rearmTierSwitch()`. No ramo em que `proxyTiers` atende, chamar `a.sender.NotifyTierSwitch(name, model)`. Esse ramo só acontece depois que o tier 0 foi pulado (cooldown) ou falhou, então é sempre uma troca.
- `history_test.go`, `alertApp`: o tier 0 dele sempre falha e um extra atende, então os 7 testes que usam `wantAlerts` receberiam o aviso novo como inesperado. Depois de `newApp`, desarmar o aviso de troca (`app.sender.switchDisarmed = true`, com comentário) para esses testes continuarem cobrindo só o primeiro uso do tier pago.
- Teste novo (em `main_test.go`, perto de `TestFailoverAllKeysExhaustedAndTiersFailStillAlerts`, usando `failoverApp` + stub de `sendMail`): tier 0 esgotado (429 quota) e o extra atendendo (200). A 1ª request gera exatamente um e-mail com "switched tier" e o nome do tier. A 2ª não gera nenhum. Quando o tier 0 volta a atender (avançar `now` além do cooldown, stub 200), sai e-mail nenhum; com o tier 0 falhando de novo, a próxima request servida pelo extra gera outro aviso. Filtrar os e-mails por "switched tier", porque `NotifySwitch` e `NotifyAllExhausted` também passam por `sendMail`. Mais um teste curto: com `sendMail` falhando, a troca seguinte manda o aviso de novo.
- `docs/configuration.md`: na seção de SMTP/notificações, uma frase sobre o aviso de troca de tier (uma vez por episódio, rearma quando o tier 0 atende).

Execução (2026-11-14):
- Arquivos: `notify.go` (`NotifyTierSwitch`, `rearmTierSwitch`, `configured`, `send` devolve `bool`), `proxy.go` (rearme no tier 0 servindo, aviso no ramo de `proxyTiers`), `history_test.go` (`alertApp` desarma o aviso), `main_test.go` (`TestTierSwitchMailOncePerEpisode`, `TestTierSwitchMailFailedSendRearms`, `TestTierSwitchMailNotConfiguredSendsNothing`), `docs/configuration.md`.
- Conferido: sem o rearme em `proxy.go`, `TestTierSwitchMailOncePerEpisode` falha.
- Surpresa: com `-race`, o teste novo acusou corrida entre a goroutine do `NotifySwitch` (alerta de chave) e o `t.Cleanup` que restaura `sendMail`. O helper `tierSwitchMails` agora drena as chamadas até 200 ms de silêncio antes de restaurar. `go test -race` passou 3 vezes seguidas.

## Plan Change Log

## Review Triage Log

### 2026-11-14 — Review pass
- verdicts: 4 findings — high 0, medium 0, low 4, false 0, maybe-false 0
- findings:
  - `[low]` `[patch]` `docs/smtp.md` e o bullet de SMTP do `README.md` não citam o aviso de troca de tier. Os dois ganharam a menção.
  - `[low]` `[patch]` O parágrafo novo em `docs/configuration.md` ficava entre o texto do Telegram e o "To try it by hand" do fakeupstream. Virou parágrafo próprio depois dele.
  - `[low]` `[patch]` `TestTierSwitchMailNotConfiguredSendsNothing` não checava que nada desarma. O teste agora afirma `switchDisarmed == false` sob o lock.
  - `[low]` `[reject]` Sleep fixo de 100 ms em `TestTierSwitchMailFailedSendRearms`: é o mesmo padrão de `TestTelegramFailedSendRearms`, já aceito. Trocar por polling acrescenta laço e lock no teste, e 100 ms sobram para um stub que retorna na hora.

## Verification

**Commands:**
- `go test ./...` -- expected: tudo passa, inclusive o teste novo de troca de tier e os 7 testes de `alertApp`
- `go vet ./...` -- expected: sem avisos

## Auto Run Result

Status: built

**Resumo:** quando o tier 0 não atende (cooldown, chaves esgotadas ou falha) e um tier extra atende, o `SMTPNotifier` manda um e-mail "Switchboard Go switched tier: <tier>" com o modelo. O aviso sai uma vez por episódio: desarma ao enviar e rearma quando o tier 0 atende de novo ou quando o envio falha. Sem SMTP configurado, nada desarma.

**Arquivos:**
- `notify.go`: `NotifyTierSwitch`, `rearmTierSwitch`, `configured`; `send` devolve `bool`.
- `proxy.go`: rearme no retorno do tier 0 e aviso no ramo em que `proxyTiers` atende.
- `main_test.go`: três testes novos (uma vez por episódio com rearme, envio falho rearma, sem configuração não desarma) e o helper `tierSwitchMails`, que drena as goroutines antes de restaurar `sendMail`.
- `history_test.go`: `alertApp` desarma o aviso para manter os testes de primeiro uso do tier pago.
- `docs/configuration.md`, `docs/smtp.md`, `README.md`: descrevem o aviso.

**Revisão:** 3 patches low aplicados, nenhum adiado, 1 rejeitado (sleep fixo no teste de envio falho, mesmo padrão do teste do Telegram). Patches por veredito: high 0, medium 0, low 3.

**Follow-up review recomendado:** false. Nenhum patch high nem dois medium.

**Verificação:** `go vet ./...` limpo; `go test -race -count=1 ./...` ok (3 vezes antes dos patches, 1 depois). Sem o rearme em `proxy.go`, `TestTierSwitchMailOncePerEpisode` falha.

**Riscos residuais:** sem chave do tier 0 configurada, toda request vai a um extra e o aviso sai uma vez e nunca rearma; é o comportamento esperado de "uma vez por episódio". Não houve envio real por SMTP, só pelo stub de `sendMail`.

## Code Review

### 2026-11-14

2 decision-needed (resolvidas → patch), 2 patch, 0 defer, 13 rejeitados.

- [x] [Review][Decision] Sem backoff com SMTP fora do ar — resolvida (Eduardo): (b) intervalo mínimo entre tentativas depois de uma falha. Vira patch.
- [x] [Review][Decision] Troca entre extras (t1 → t2) dentro do episódio não avisa — resolvida (Eduardo): (b) avisar também a mudança de tier extra dentro do episódio. Vira patch.
- [x] [Review][Patch] Depois de um envio falho, esperar um intervalo mínimo antes de tentar de novo; o rearme por falha só vale se o episódio ainda é o mesmo [notify.go:NotifyTierSwitch]
- [x] [Review][Patch] Avisar cada tier extra novo que atende dentro do episódio (uma vez por tier, para tráfego misto entre t1 e t2 não mandar e-mail a cada request) [notify.go:NotifyTierSwitch]
- [x] [Review][Patch] 404 model_not_found no tier 0 saudável conta como troca: um modelo só mapeado num extra manda "switched tier" com o tier 0 de pé, e tráfego misto rearma e repete o e-mail [proxy.go:226]
- [x] [Review][Patch] Docs sem as limitações: restart no meio do episódio manda outro aviso; sem chave no tier 0 o aviso sai uma vez e nunca rearma [docs/configuration.md:254]

Rejeitados:
- Rearme tardio de envio falho do episódio anterior: low, custa um e-mail a mais num cenário raro; o conserto pede contador de geração.
- Mutex no caminho quente: low, mutex sem contenção custa nanossegundos.
- Dois e-mails (troca + primeiro uso do tier pago): false, são alertas distintos e documentados; a escrita do campo no teste vem antes de qualquer goroutine.
- E-mail sem o motivo da falha do tier 0 e sem aviso de volta: low, melhoria que pede parâmetro e e-mail novos.
- Testes por tempo e lacunas (concorrência, stream): low, mesmo padrão dos testes do Telegram.
- Inconsistências do plano (estimativa, `ticket: '6'`, Change Log vazio): false, convenção do store e registro histórico.
- 4xx de cliente no tier 0 rearma: false, o tier 0 respondeu; o Telegram trata 4xx igual.
- Requests concorrentes fora de ordem perto da recuperação: low, a request de fato trocou de tier; no máximo um e-mail a mais.
- Dreno de 200 ms no cleanup do teste: low, as goroutines nascem antes de `chatReq` voltar.
- Sleep de 100 ms no teste de envio falho: low, já rejeitado no build.
- Claim "rearma quando o tier 0 volta a atender" também em 4xx: false, mesmo motivo do 4xx.
- `alertApp` mascara a interação com o alerta de primeiro uso: low, os alertas são independentes.
- Cooldown e falha do tier 0 também contam como troca: false, "esgotado" é o caso do verify, não o limite da intenção.
