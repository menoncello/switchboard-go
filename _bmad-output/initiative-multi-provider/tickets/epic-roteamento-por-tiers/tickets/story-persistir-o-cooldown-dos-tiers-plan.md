---
title: 'Persistir o cooldown dos tiers'
type: 'feature'
ticket: 7
created: '2026-10-29'
status: 'built'
baseline_revision: '986fc6795e179e46369de062e5c2a50e54293785'
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

**Problem:** O cooldown dos tiers (e o do tier 0) e o estado esgotado das chaves do tier 0 vivem só em memória; um reinício do proxy faz o Eduardo voltar a bater em tier e chave que ainda estão em cooldown.

**Approach:** `state_file` na config (YAML, top-level). Quando definido, o proxy grava um JSON com os cooldowns vigentes e as chaves esgotadas (por prefixo do SHA-256), com escrita atômica, a cada mudança, e carrega o arquivo em `newApp`. Sem `state_file`, nada é gravado e a partida avisa.

## Boundaries & Constraints

**Always:** Escrita atômica (arquivo temporário no mesmo diretório + rename). Chave identificada por um prefixo do SHA-256 do valor, nunca pelo valor nem só pelo índice. Entrada vencida não é restaurada nem regravada. Falha de escrita só é logada (sem o valor de chave) e nunca afeta a request. Arquivo ausente ou ilegível na partida: segue sem estado, com log. `state_file` só vem do YAML. Documentar `state_file` em `docs/configuration.md` e o volume do Docker em `docs/docker.md`, com a permissão de escrita do usuário `nonroot`.

**Never:** Banco de dados. Persistir o gasto do dia (épico de relatório). Gravar o valor de uma chave. Mudar a lógica de cooldown ou de rotação de chaves. Variável de ambiente para `state_file`.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Cooldown sobrevive | tier em cooldown, app recriado com o mesmo `state_file` | tier segue em cooldown (pulado) | — |
| Cooldown vencido | arquivo com `until` no passado | tier elegível, nada restaurado | — |
| Chave do tier 0 esgotada | chave esgotada dentro do cooldown, app recriado | chave volta esgotada, achada pelo hash, mesmo se a ordem das chaves mudar | — |
| Sem `state_file` | `state_file` vazio | nenhum arquivo gravado; aviso na partida | — |
| Falha de escrita | diretório inexistente ou sem permissão | request segue normal; log de erro | só log |
| Arquivo corrompido | JSON inválido | partida segue sem estado; log | só log |

</intent-contract>

## Code Map

- `cooldown.go` -- `tierCooldowns` (`until` por nome, `now` injetável; o tier 0 usa a chave `tier0CooldownKey` = ""). Ganha `snapshot()` (só vigentes) e `restore()` (só no futuro).
- `keys.go` -- `KeyManager` (`states`, `last429`, `cooldown`, `now`). Ganha o ID da chave (prefixo SHA-256) e `snapshot()`/`restore()` das esgotadas dentro do cooldown; `cooldown == 0` não persiste nada.
- `server.go` -- `newApp` carrega o estado; `ServeHTTP` chama `saveState` ao fim dos casos proxy e admin (os resets e `validate-keys` mexem nas chaves). `saveState` serializa, ignora se os bytes não mudaram desde a última gravação bem-sucedida e escreve atômico.
- `config.go` -- `Config.StateFile`, `yamlConfig.state_file`, `mergeConfig`; `warnNoStateFile` ao lado de `warnIneligibleTiers`.
- `main.go` -- chama `warnNoStateFile` onde chama `warnIneligibleTiers`.
- `main_test.go` -- fixtures de tier existentes (`tierOK`, testes de failover) como modelo para recriar o app.
- `docs/configuration.md`, `docs/docker.md` -- documentação.

## Tasks & Acceptance

**Execution:**
- [ ] `config.go`, `main.go` -- `StateFile` (YAML `state_file`, merge) e `warnNoStateFile`, chamado na partida e no `validate-config`
- [ ] `cooldown.go`, `keys.go` -- `snapshot`/`restore` dos cooldowns e das chaves esgotadas (hash por prefixo do SHA-256)
- [ ] `server.go` -- `saveState` (JSON, deduplicado, escrita atômica, erro só logado), carga em `newApp`, chamada ao fim das requests de proxy e admin
- [ ] `main_test.go` -- cobrir a matriz: sobrevive a recriar o app, vencido volta elegível, chave achada pelo hash e arquivo sem o valor da chave, sem `state_file` nada gravado e aviso, falha de escrita não derruba a request, arquivo corrompido
- [ ] `docs/configuration.md`, `docs/docker.md` -- `state_file` (YAML, sem env, aviso sem ele) e o volume (diretório gravável por UID 65532 do `nonroot`)

**Acceptance Criteria:**
- Given um tier em cooldown e `state_file` definido, when o app é recriado a partir do arquivo, then o tier continua em cooldown.
- Given um cooldown já vencido no arquivo, when o app é recriado, then o tier está elegível.
- Given `state_file` vazio, when o proxy atende requests, then nenhum arquivo é gravado e a partida avisou.
- Given um `state_file` que não pode ser gravado, when uma request muda um cooldown, then o cliente recebe a resposta normal.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 7 findings — high 0, medium 0, low 7, false 0, maybe-false 0
- findings:
  - `[low]` `patch` caminho de escrita ruim loga a cada request — `lastState` agora também guarda a tentativa que falhou, então loga uma vez por mudança
  - `[low]` `reject` escrita em disco no caminho crítico sob `stateMu` — o arquivo tem poucas centenas de bytes; trocar por goroutine de fundo soma complexidade sem defeito demonstrado
  - `[low]` `reject` nomes e horários restaurados não validados contra a config — só um arquivo editado à mão causaria isso, e o dado vence sozinho ao expirar
  - `[low]` `reject` temp file órfão e sem `fsync` — arquivo corrompido ou vazio é tratado na partida (segue sem estado, com log); durabilidade contra queda de energia não é requisito
  - `[low]` `patch` docs não diziam que `retry_exhausted_after: 0` não persiste chaves — frase adicionada em `docs/configuration.md`
  - `[low]` `patch` linha longa em `docs/configuration.md` — reenquadrada
  - `[low]` `reject` testes faltando (cooldown 0, reset admin, aviso via `main`) — o comportamento é coberto por leitura direta do código e a matriz do plano está coberta; mais testes só somam churn

## Auto Run Result

- Mudança: `state_file` (YAML) grava um JSON com os cooldowns vigentes (tier 0 e extras) e as chaves esgotadas do tier 0, identificadas por 16 hex do SHA-256, com escrita atômica; `newApp` carrega e ignora o que já venceu. Sem `state_file`, nada é gravado e a partida avisa.
- Arquivos: `config.go` (campo, YAML, merge, `warnNoStateFile`), `main.go` (aviso na partida e no `validate-config`), `cooldown.go` e `keys.go` (`snapshot`/`restore`, `keyID`), `server.go` (`loadState`, `saveState`, `writeFileAtomic`, gravação ao fim das requests de proxy e admin), `main_test.go` (8 testes novos), `docs/configuration.md` e `docs/docker.md`.
- Review: 7 achados low; 3 patches aplicados, 4 rejeitados (motivos no log).
- Follow-up recomendado: não.
- Verificação: `go build`, `go vet` e `go test -race -count=1 ./...` passam, antes e depois dos patches.
- Risco residual: a escrita em disco roda no fim da request, sob mutex; um disco lento atrasa o fim das requests. Permissão do `nonroot` no volume documentada, não testada em container real.

## Design Notes

Formato: `{"tier0": "<RFC3339>", "tiers": {"nome": "<RFC3339>"}, "keys": {"<16 hex>": "<RFC3339 do último 429>"}}`. O `saveState` pula a gravação quando os bytes são iguais aos da última gravação bem-sucedida, então `MarkAvailable` a cada request bem-sucedida não vira escrita em disco. Gravar ao fim da request (e não no instante da mudança) é suficiente: o que vale é sobreviver a reinício, e a request seguinte regrava tudo.

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test -race -count=1 ./...` -- expected: todos passam, incluindo os novos
