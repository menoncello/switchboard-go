---
title: 'Alerta SMTP no primeiro uso de tier pago do dia'
type: 'feature'
ticket: 7
created: '2026-10-30'
status: 'built'
baseline_revision: '0ff00ded635fd687aff09584f37bab33db424bf0'
route: 'full'
route_source: 'auto'
risk: 'low'
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

**Problem:** Um tier pago passa a gastar dinheiro sem que o Eduardo saiba que entrou em uso naquele dia (R5).

**Approach:** A primeira request atendida por um tier pago em cada dia (fuso `timezone`) envia um e-mail SMTP e grava o dia do aviso em `usage_file`, para não repetir após reinício. Falha de envio só é logada e não afeta a request.

## Boundaries & Constraints

**Always:** um aviso por tier por dia, no dia de `history.loc`; o envio é assíncrono (como os outros avisos de `SMTPNotifier`); o dia fica marcado antes do envio, então falha de SMTP não gera nova tentativa; `rows` do `usage_file` mantêm os mesmos campos, o dia do aviso entra em chave nova de topo `alerts` (tier → `YYYY-MM-DD`); arquivo antigo sem `alerts` carrega normalmente; sem SMTP configurado o envio é no-op (o `send` existente já faz isso); só conta request atendida e gravada no histórico (a mesma condição de `record`).

**Never:** alerta para tier gratuito; alerta na troca de tier (adiado, `deferred-work.md`); alterar os campos do log por request ou das linhas de `rows`; novo campo de config; tentar de novo envio que falhou; Telegram.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Primeiro uso do dia | request atendida por tier pago, sem aviso hoje | 1 e-mail com o tier e o modelo; `alerts[tier]` = hoje | — |
| Segunda request | mesmo tier, mesmo dia | nenhum e-mail novo | — |
| Reinício | app recriado com o mesmo `usage_file` | nenhum e-mail novo no mesmo dia | — |
| Dia seguinte | `now` passa da meia-noite do fuso | 1 e-mail novo | — |
| Tier gratuito | request atendida por tier `free: true` | nenhum e-mail | — |
| Dois tiers pagos | cada um com a primeira request do dia | 1 e-mail por tier | — |
| Falha de SMTP | `sendMail` devolve erro | request já respondida normalmente; erro só no log; sem reenvio no dia | log `smtp notification failed` |
| Arquivo sem `alerts` | `usage_file` do formato anterior | carrega; primeiro uso do dia avisa | — |
| Request não atendida | falha sem tier, GET `/v1/models`, request em hold | nenhum e-mail | — |

</intent-contract>

## Code Map

- `history.go` -- `usageHistory` (`mu`, `rows`, `dirty`, `timer`, `loc`, `now`), `load` (l.~65, lê `{"rows": ...}`), `record` (l.~122, agenda o flush atrasado com `time.AfterFunc` quando `dirty`), `flush` (l.~210, escreve `{"rows": snapshotLocked()}`; `close` chama `flush`). Entram o campo `alerts map[string]string` (inicializar em `newUsageHistory`), a leitura em `load`, a escrita em `flush` e `firstUse(tier string) bool`. Extrair de `record` o trecho "dirty + agenda timer" para um helper `markDirtyLocked()` e reusar nos dois. `flush` só escreve se `dirty`, então `firstUse` marca `dirty`.
- `notify.go` -- `SMTPNotifier` (`NotifySwitch`, `NotifyAllExhausted` fazem `go n.send(...)`; `send` ignora config SMTP vazia e loga `smtp notification failed`), `sendMail` é var substituível nos testes. Entra `NotifyPaidFirstUse(tier, model string)`.
- `proxy.go` -- `proxyV1` logo após `a.history.record(...)` (l.~141), dentro do `if tier != "" && !held && POST && model != ""`: se `a.config.paidTier(tier) && a.history.firstUse(tier)` chama `a.sender.NotifyPaidFirstUse(tier, model)`. `Config.paidTier` já existe (config.go l.~583).
- `main_test.go` l.~2569 -- exemplo de teste que troca `sendMail` e restaura com `t.Cleanup`; `history_test.go` -- `historyAppWith`, `post`, `tierReq`, `capApp`, helpers de relógio fixo (`app.history.now`) e de recriar o app (`close` + novo `newApp`) do ticket 3.6.
- `docs/configuration.md` (seções "Usage history" e SMTP), `README.md` se citar avisos SMTP.

## Tasks & Acceptance

**Execution:**
- [ ] `history.go` -- campo `alerts`, `load` e `flush` com a chave `alerts`, `firstUse(tier)` (true uma vez por tier por dia de `h.loc`, marca `dirty` e agenda o flush), helper `markDirtyLocked` -- R5
- [ ] `notify.go` -- `NotifyPaidFirstUse(tier, model)`: assunto e corpo dizem o tier, o modelo e o dia; envio assíncrono por `send` -- R5
- [ ] `proxy.go` -- chamar `firstUse` e `NotifyPaidFirstUse` após `record` para tier pago -- R5
- [ ] `history_test.go` -- matriz inteira com `sendMail` falso que conta chamadas (canal com timeout, o envio é assíncrono): primeiro uso envia, segunda não, recriar app não reenvia, dia seguinte envia, tier gratuito nunca, dois tiers pagos enviam um cada, `sendMail` com erro não muda a resposta nem reenvia, arquivo sem `alerts` carrega, `rows` do arquivo salvo seguem iguais -- R5
- [ ] `docs/configuration.md`, `README.md` -- o aviso de primeiro uso do tier pago por dia, onde fica gravado (`alerts` no `usage_file`) e que usa a config `smtp` -- R5

**Acceptance Criteria:**
- Given tier pago e SMTP falso, when a primeira request do dia é atendida, then um e-mail sai e a segunda no mesmo dia não envia outro.
- Given o aviso do dia gravado em `usage_file`, when o app é recriado, then o mesmo dia não reenvia.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-30 — Review pass
- verdicts: 5 findings — high 0, medium 0, low 5, false 0, maybe-false 0
- findings:
  - `[low]` `[patch]` e-mail usava `time.Now()` local em vez do dia de `history.loc` — `firstUse` passou a devolver o dia e `NotifyPaidFirstUse` o recebe.
  - `[low]` `[reject]` teste de `rows` só conta linhas — o formato das linhas não foi tocado no diff; o conserto é só mais asserção.
  - `[low]` `[reject]` sem teste de request em hold — a guarda `!held` já existia e vale para o `record`; improvável de regredir.
  - `[low]` `[reject]` teste de reinício chama `flush` em vez de `close` — `close` já chama `flush` e tem teste próprio do ticket 3.2.
  - `[low]` `[reject]` `alerts` de tiers removidos ficam no arquivo — limitado ao número de tiers já configurados.

## Auto Run Result

**Resumo:** a primeira request atendida por um tier pago em cada dia (fuso `timezone`) envia e-mail SMTP e grava o dia em `alerts` no `usage_file`, então um reinício no mesmo dia não reenvia. Falha de SMTP só é logada e não é repetida.

**Arquivos:**
- `history.go` -- `alerts`, leitura/escrita em `usage_file`, `firstUse`, `markDirtyLocked`
- `notify.go` -- `NotifyPaidFirstUse`
- `proxy.go` -- dispara o alerta após `record` em tier pago
- `history_test.go` -- 3 testes da matriz com `sendMail` falso
- `docs/configuration.md`, `docs/smtp.md`, `README.md` -- aviso e chave `alerts`

**Revisão:** 5 achados, 1 patch aplicado (dia do e-mail no fuso configurado), 0 adiados, 4 rejeitados (testes mais fortes e limpeza de `alerts`, todos low).
**Follow-up recomendado:** false.
**Verificação:** `go vet ./...` e `go test -race -count=1 ./...` verdes após o patch. Não enviei e-mail a servidor SMTP real.
**Risco residual:** crash antes do flush atrasado (3 s) repete o e-mail no reinício; `alerts` de tiers removidos permanece no arquivo.

## Design Notes

O dia fica marcado antes do envio: o contrato é "uma vez por dia"; reenviar em falha de SMTP exigiria estado e retry fora do escopo. O flush atrasado de poucos segundos pode perder o marcador num crash, e então o aviso se repete no reinício: aceito, é só um e-mail a mais.

## Verification

**Commands:**
- `go vet ./...` -- expected: sem aviso
- `go test -race -count=1 ./...` -- expected: verde, com os testes novos
