---
title: 'Separar main.go em arquivos do package main'
type: 'refactor'
ticket: '1'
created: '2026-10-29'
status: done
baseline_revision: '411af6caca1c2d8524481eb6724ed38294a8bfe9'
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

**Problem:** `main.go` tem 1149 linhas com config, chaves, servidor, proxy e SMTP juntos; as próximas stories do épico mexem em `KeyManager` e `proxyV1` e precisam de arquivos separados por responsabilidade.

**Approach:** Mover o código, sem editá-lo, para arquivos do `package main` por responsabilidade; `main.go` fica só com `main()`.

## Boundaries & Constraints

**Always:** Só mover declarações (corpo idêntico, mesma ordem relativa); ajustar apenas o bloco `import` de cada arquivo; `gofmt` limpo; `main_test.go` e `adminkeys.go` intactos.

**Never:** Pacotes novos, interfaces novas, renomear símbolos, mudar assinaturas ou comportamento, editar testes.

</intent-contract>

## Code Map

- `main.go` -- 1149 linhas, um só `package main`; imports únicos no topo (linhas 3-27).
- `main.go:29-344` -- `Config`, `SMTPConfig`, `loadConfig`, `defaultConfig`, `resolveConfigPath`, `yamlConfig`, `loadYAMLConfig`, `mergeConfig`, `applyEnvOverrides`, `defaultString`, `validateConfig`, `safeConfigSummary`, `parseBool` → `config.go`.
- `main.go:69-74` -- `APIStyle` e constantes → `proxy.go` (usado pelo proxy e pelo erro).
- `main.go:346-602` -- `KeyState`, `KeyManager` e métodos, `PerKeyStatus`, `StatusResponse`, `ValidateKey*` → `keys.go`.
- `main.go:604-812` -- `App`, `newApp`, `ServeHTTP`, auth, admin, readyz, `writeJSON`, `checkUpstreamReady`, `handleValidateKeys` → `server.go`.
- `main.go:814-1046` -- `proxyV1`, `doUpstream`, `apiStyleForRequest`, `validateConfigAndPrint`, `copyHeaders`, `stripHopByHopHeaders`, `copyResponse`, `isKeyDead`, `isQuota429`, `writeAPIError`, `write*Error`, `anthropicErrorType` → `proxy.go` (`validateConfigAndPrint` vai para `server.go`).
- `main.go:1048-1117` -- `SMTPNotifier`, `sendMail` → `notify.go`.
- `main.go:1119-1149` -- `main()` fica em `main.go`.
- `adminkeys.go`, `main_test.go` -- não tocar.

## Tasks & Acceptance

**Execution:**
- [x] `config.go` -- criar com o código de config -- responsabilidade config
- [x] `keys.go` -- criar com `KeyManager` e tipos de status -- responsabilidade chaves
- [x] `server.go` -- criar com `App`, handlers e `validateConfigAndPrint` -- responsabilidade servidor
- [x] `proxy.go` -- criar com `APIStyle`, `proxyV1` e helpers de upstream/erro -- responsabilidade proxy
- [x] `notify.go` -- criar com `SMTPNotifier` e `sendMail` -- responsabilidade notificação
- [x] `main.go` -- reduzir a `main()` e seus imports -- só montagem do servidor

**Acceptance Criteria:**
- Given o repositório após a mudança, when rodar `go build ./... && go vet ./... && go test ./...`, then tudo passa sem alterar `main_test.go`.
- Given `main.go`, when listar suas declarações, then só `main()` existe.
- Given `git diff --stat` sobre `*.go`, then o total de linhas removidas de `main.go` é igual ao adicionado nos novos arquivos, exceto `package`/`import`.

## Implementation Notes

Rota full: mover ~1100 linhas entre 6 arquivos, mecânico mas amplo.

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 0 findings — high 0, medium 0, low 0, false 0, maybe-false 0
- findings: none

## Verification

**Commands:**
- `gofmt -l .` -- expected: sem saída
- `go build ./... && go vet ./... && go test ./...` -- expected: sucesso
- `git diff --stat -- main_test.go adminkeys.go` -- expected: vazio

## Auto Run Result

- Summary: `main.go` dividido por responsabilidade no `package main`, código movido sem edição; `main.go` ficou só com `main()`.
- Files: `config.go` (config), `keys.go` (KeyManager e tipos de status), `server.go` (App, handlers, `validateConfigAndPrint`), `proxy.go` (`APIStyle`, `proxyV1`, helpers de upstream e erro), `notify.go` (SMTP), `main.go` (só `main()`).
- Review: revisão quick, 0 achados; nenhum patch, defer ou rejeição.
- Follow-up review recommended: false (nenhum patch aplicado).
- Verification: `gofmt -l .` vazio; `go build`, `go vet`, `go test` passam; `main_test.go` e `adminkeys.go` intactos; comparação das linhas ordenadas mostra só diferenças de `package`/`import`.
- Residual risks: nenhum conhecido.

## Code Review

### 2026-10-29
0 decision-needed, 1 patch, 5 defer, 8 rejected.

- [x] [Review][Patch] Plan: Execution checkboxes unticked although status is built [story-separar-main-go-em-arquivos-do-package-main-plan.md:51]
- [x] [Review][Defer] proxyV1 devolve o erro de transporte cru ao cliente (vaza a URL do upstream, ignora o formato por estilo) [proxy.go:40] — deferred: pre-existing
- [x] [Review][Defer] Valores inválidos de MAX_REQUEST_BODY_BYTES e SMTP_PORT são ignorados em silêncio [config.go:223,262] — deferred: pre-existing
- [x] [Review][Defer] Código morto carregado na divisão: validateConfigAndPrint e KeyManager.AdvanceOnExhaustion [server.go:226, keys.go:105] — deferred: pre-existing, a story só move código
- [x] [Review][Defer] yamlConfig não tem usage_url nem usage_cache_ttl (só por variável de ambiente) [config.go:96] — deferred: pre-existing
- [x] [Review][Defer] proxyV1 detecta body grande por strings.Contains no texto do erro [proxy.go:24] — deferred: pre-existing

#### Rejected
- Log de startup duplica safeConfigSummary / validateConfig chamado duas vezes (low): os textos diferem, unir muda a saída; a validação dupla é inofensiva.
- Code Map do plano com linhas desatualizadas (low): documento histórico, reescrever não vale.
- "Comparação das linhas ordenadas" como verificação fraca (false): a diferença ordenada entre o main.go antigo e os arquivos novos é vazia, só mudam `package`/`import`.
- Log de review sem evidência e sem plano de conflito de merge (low): comentário de processo, sem defeito no código.
- Falta `.git-blame-ignore-revs` (low): raramente visível, exige arquivo extra.
- Divisão de responsabilidade incompleta (adminkeys.go, tipos ValidateKey*) (low): o plano proíbe mexer em adminkeys.go; corrigir editaria a intenção.
- isQuota429 trata "exhausted" como cota (low): heurística intencional.
- handleValidateKeys repete Discard+Close três vezes (low): duplicação cosmética.
