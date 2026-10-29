---
title: 'Separar main.go em arquivos do package main'
type: 'refactor'
ticket: '1'
created: '2026-10-29'
status: 'built'
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
- [ ] `config.go` -- criar com o código de config -- responsabilidade config
- [ ] `keys.go` -- criar com `KeyManager` e tipos de status -- responsabilidade chaves
- [ ] `server.go` -- criar com `App`, handlers e `validateConfigAndPrint` -- responsabilidade servidor
- [ ] `proxy.go` -- criar com `APIStyle`, `proxyV1` e helpers de upstream/erro -- responsabilidade proxy
- [ ] `notify.go` -- criar com `SMTPNotifier` e `sendMail` -- responsabilidade notificação
- [ ] `main.go` -- reduzir a `main()` e seus imports -- só montagem do servidor

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
