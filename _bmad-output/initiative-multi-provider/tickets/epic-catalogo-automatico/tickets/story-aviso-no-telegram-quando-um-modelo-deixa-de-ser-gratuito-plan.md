---
title: 'Aviso no Telegram quando um modelo deixa de ser gratuito'
type: 'feature'
ticket: 6
created: '2026-10-31'
status: done
baseline_revision: '62be3207b513a2acf333982973d668bf1066c54c'
route: 'oneshot'
route_source: 'auto'
risk: 'low'
review: 'quick'
review_source: 'pinned'
lenses_ran: ['quick']
review_loop_iteration: 0
followup_review_recommended: true
context: ['_bmad-output/initiative-multi-provider/tickets/epic-catalogo-automatico/epic-catalogo-automatico.md']
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** Um modelo que passou de gratuito a pago só aparece no arquivo da proposta; o Eduardo pode gastar sem querer antes de abri-lo.

**Approach:** Em `docs/catalog-agent.md`, depois de gravar a proposta e o "Roteamento", o agente lê `telegram.bot_token` e `telegram.chat_id` da config (só leitura) e envia uma mensagem com os modelos de "Gratuidade" que ficaram como "gratuito para pago" e o caminho da proposta. Sem esse item não envia nada.

</intent-contract>

## Implementation Notes

Mudança só de texto em `docs/catalog-agent.md` (~25 linhas), então oneshot; o proxy não muda e `telegram.go` não é reaproveitado (o agente roda fora do proxy). Novo passo 8, depois do guarda de roteamento, para o hash "fim" não depender do envio. O aviso usa só os itens que sobraram do passo 6, então um modelo já avisado numa rodada anterior não gera novo aviso. A leitura de `telegram.*` vai para o passo 1 e `bot_token` entra na lista de segredos que nunca saem em log, proposta ou chat. Envio: `POST https://api.telegram.org/bot<token>/sendMessage` com `chat_id` e `text`; falha de envio vai para o chat sem o token e não invalida a proposta. Sem `bot_token` ou `chat_id`, não envia e avisa no chat. A verificação de ponta a ponta (mensagem real no Telegram) é `hitl`, do Eduardo.

## Verification

**Commands:**
- `go test ./... && go vet ./...` -- expected: sem erro
- `git diff --stat -- . ':!_bmad-output'` -- expected: só `docs/catalog-agent.md`

**Manual checks (if no CLI):**
- Inspeção do texto: o passo 8 só envia com item "gratuito para pago" na proposta, cita modelo e caminho do arquivo, não imprime o token e não altera a config.
- (hitl) Uma proposta com um modelo passando de gratuito a pago faz chegar uma mensagem no Telegram com o modelo e o caminho; uma rodada sem essa mudança não envia mensagem.

### 2026-10-31 — Review pass
- verdicts: 5 findings — high 0, medium 4, low 1, false 0, maybe-false 0
- findings:
  - `[medium]` `[patch]` Envio falho ou pulado nunca é refeito, porque o passo 6 descarta o item na rodada seguinte — o aviso de falha no chat agora nomeia os modelos e diz que rodadas futuras não reenviam
  - `[medium]` `[patch]` O token ia na URL do POST e podia aparecer na chamada da ferramenta — passo 8 exige ferramenta que não mostre URL/token, senão não envia e avisa no chat
  - `[low]` `[patch]` Passo 7 ainda dizia "then finish" e a linha de borda do OpenRouter citava passos 5, 6 e 7 — apontam para o passo 8
  - `[medium]` `[patch]` Proposta incompleta (falha ao gravar "Roteamento") ainda podia gerar aviso — passo 8 não envia nesse caso
  - `[medium]` `[patch]` Texto da mensagem ambíguo (caminho por modelo ou uma vez, caminho relativo, limite de 4096 caracteres) — uma linha por modelo (até 20, depois `e mais N`), caminho absoluto uma vez

## Auto Run Result

**Summary:** novo passo 8 em `docs/catalog-agent.md`: depois do "Roteamento", o agente envia uma mensagem no Telegram (`bot_token`/`chat_id` lidos da config, só leitura) com os modelos de "Gratuidade" que ficaram como "gratuito para pago" e o caminho da proposta; sem esse item não envia nada.

**Files changed:**
- `docs/catalog-agent.md` — leitura de `telegram.*` no passo 1, segredo `bot_token`, passo 8, linhas de borda e acesso a `api.telegram.org` no Orca

**Review:** 5 patches aplicados (medium 4, low 1), 0 adiados, 0 rejeitados.

**Follow-up review recommended:** true — 4 `medium` corrigidos em texto de doc; risco não verificado: se a ferramenta do agente consegue fazer o POST sem mostrar o token.

**Verification:** `go test ./... && go vet ./...` ok; só `docs/catalog-agent.md` mudou fora de `_bmad-output`. A mensagem real no Telegram (verify `hitl`) não foi executada.

**Residual risks:** o item perdido por falha de envio não é reenviado em rodadas futuras; o envio nunca rodou com um agente.

## Code Review

### 2026-10-31
Code review: 3 decision-needed, 2 patch, 0 defer, 11 rejected.

- [x] [Review][Decision] Aviso perdido de vez se o envio falha ou é pulado — o passo 6 descarta o item nas rodadas seguintes. Options: (a) best-effort, manter e documentar; (b) marcador no arquivo da proposta e exceção no passo 6 para reenviar. Recommended: (a) — só vale (b) se ninguém lê o chat do agente. Resolved: (a) best-effort.
- [x] [Review][Decision] Token pode aparecer na chamada da ferramenta (URL com `bot<token>` no argumento) ou em erro que repete a URL — Options: (a) só enviar se a ferramenta aceitar o token fora da linha de comando (stdin/env); (b) aceitar o token no transcript e documentar. Recommended: (a) — o token é segredo. Resolved: (a).
- [x] [Review][Decision] Reaviso quando só o preço muda e o modelo continua gratuito→pago (item volta com `antes proposto`) — Options: (a) reavisar; (b) pular itens cujo `antes proposto` já era gratuito para pago. Recommended: (b) — o modelo já foi avisado. Resolved: (b).
- [x] [Review][Patch] Linhas de borda "config changes" ("The run ends") e "page unreadable" não mencionam o passo 8; dizer que o aviso segue normalmente [docs/catalog-agent.md:Edge cases]
- [x] [Review][Patch] Formato e envio: cabeçalho único + uma linha por modelo, corpo serializado como JSON, timeout, `ok:false` ou corpo ilegível = falha [docs/catalog-agent.md:step 8]

Rejected: 11 (false: schema `telegram` já definido em `config.go:209` e `docs/configuration.md:77`; volta a gratuito e pago de novo já muda o último valor no passo 6; gratuidade não verificada fora da intenção; baixa: setup sem teste do Telegram, o `hitl` cobre).
