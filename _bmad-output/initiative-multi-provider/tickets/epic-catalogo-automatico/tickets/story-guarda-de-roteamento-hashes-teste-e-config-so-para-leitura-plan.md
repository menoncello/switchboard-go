---
title: 'Guarda de roteamento: hashes, teste e config só para leitura'
type: 'feature'
ticket: 2
created: '2026-10-30'
status: 'done'
baseline_revision: '01cf68914e7355eada0ca1858a324ce12f3b3ba4'
route: 'full'
route_source: 'auto'
risk: 'medium'
review: 'quick'
review_source: 'pinned'
lenses_ran: ['quick']
review_loop_iteration: 0
followup_review_recommended: false
context: ['_bmad-output/initiative-multi-provider/tickets/epic-catalogo-automatico/epic-catalogo-automatico.md']
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** Nada prova que uma rodada do agente de catálogo deixou o roteamento como estava, e nada impede que o proxy passe a ler o diretório de propostas.

**Approach:** O agente grava na proposta o hash da config, do `state_file` e do `usage_file` (os que alcança) do início e do fim da rodada, com a linha "roteamento inalterado: sim/não" que depende só do hash da config. Um teste em Go falha se código do proxy (fora de `_test.go`) citar `catalog-proposals`. `docs/catalog-agent.md` documenta a config só para leitura no Orca.

## Boundaries & Constraints

**Always:** Hash SHA-256 dos bytes do arquivo, em hexadecimal. A linha "roteamento inalterado" compara só o hash da config (o proxy reescreve `state_file` e `usage_file` sozinho); os hashes dos outros dois são informativos. Arquivo que o agente não alcança (caminho vazio na config ou inacessível) entra como `não alcançável`, sem hash. Os hashes entram em seção própria "Roteamento", depois das cinco seções existentes. O hash do fim é calculado depois de gravar a proposta. O teste ignora `_test.go` e diretórios que começam com `.` ou `_`.

**Never:** Alterar código do proxy, a config de tiers ou `prices`. Testar a saída do agente byte a byte. Mexer em detecção de item repetido, páginas de documentação, Telegram, provedores novos ou agendamento (stories 4.3 a 4.7). Fazer o agente criar diretórios ou ganhar escrita na config.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Rodada normal | Config igual no início e no fim | Seção "Roteamento" com os 3 hashes de início e de fim e `roteamento inalterado: sim` | Sem erro |
| Config muda na rodada | Hash da config do fim difere do início | `roteamento inalterado: não`, com os dois hashes | Rodada termina, sem reverter nada |
| Proxy reescreve `state_file` | Só `state_file` ou `usage_file` mudam | Hashes diferentes nessa linha, `roteamento inalterado: sim` | Sem erro |
| Arquivo não alcançável | `state_file` ou `usage_file` vazio ou sem acesso | `não alcançável` no lugar do hash | Sem erro |
| Config ilegível no fim | Config some ou não abre ao final | Hash do fim `ilegível` e `roteamento inalterado: não` | Sem erro |
| Proxy cita as propostas | `.go` do proxy com `catalog-proposals` | Teste falha, com o arquivo e a linha | Falha do teste |

</intent-contract>

## Code Map

- `docs/catalog-agent.md` -- passos da rodada (36-73), formato (75-134), tabela de bordas (136-147) e "Setting it up in Orca" (149-158); acrescentar hashes e seção "Roteamento", ajustar "cinco seções" para seis, reforçar config só para leitura
- `catalog_guard_test.go` -- novo, `package main`; teste da guarda (estilo dos `*_test.go` da raiz, ex.: `history_test.go`)
- `config.go:54-60,216-217` (`StateFile`, `UsageFile`, `state_file`, `usage_file`) -- origem dos dois caminhos; não alterar
- `docs/configuration.md` -- onde `state_file` e `usage_file` são descritos; só referência, sem mudar

## Tasks & Acceptance

**Execution:**
- [x] `catalog_guard_test.go` -- `TestProxyDoesNotReferenceCatalogProposals`: percorrer a raiz com `filepath.WalkDir`, pular diretórios `.*` e `_*` e arquivos `_test.go`, ler cada `.go` e falhar com `arquivo:linha` se achar `catalog-proposals` -- prova que o proxy não lê as propostas
- [x] `docs/catalog-agent.md` -- (a) passo novo no início da rodada: hash SHA-256 de config, `state_file`, `usage_file`; (b) passo final: refazer os hashes depois de gravar a proposta; (c) seção "Roteamento" no formato, com linhas de início/fim e `roteamento inalterado: sim|não` só pela config; (d) bordas da matriz; (e) "cinco seções" vira seis; (f) passo do Eduardo no Orca: config só para leitura, com a explicação do porquê -- guarda de roteamento e documentação
- [x] `go test ./... && go vet ./...` -- rodar -- confirma que o código atual passa

**Acceptance Criteria:**
- Given um `.go` do proxy fora de `_test.go` que cite `catalog-proposals`, when `go test` roda, then o teste falha apontando o arquivo; given o código atual, then passa.
- Given uma rodada, when a proposta é gravada, then a seção "Roteamento" traz hash de início e de fim da config, do `state_file` e do `usage_file`, e `roteamento inalterado: sim|não`.
- Given que só o `state_file` ou o `usage_file` mudam na rodada, when a linha é escrita, then ela segue `sim`; given que a config muda, then `não`.
- Given `git diff`, when a story termina, then só `catalog_guard_test.go` e `docs/catalog-agent.md` mudaram.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-30 — Review pass
- verdicts: 5 findings — high 0, medium 2, low 3, false 0, maybe-false 0
- findings:
  - `[low]` `[patch]` Arquivo alcançável só numa ponta sem regra — `não alcançável` só do lado afetado, definido no passo 6 e nos bullets
  - `[medium]` `[patch]` Passo 5 dizia "finish" antes do passo 6 — agora segue para o passo 6; o caso "sem tier OpenRouter" também passa por 5 e 6
  - `[low]` `[patch]` Hash inicial com config ilegível indefinido — passo 0 manda parar e seguir o caso "Config unreadable"
  - `[medium]` `[patch]` Parágrafo após a tabela só cobria quatro casos — reescrito: todo caso, menos "Config unreadable", termina com a proposta e o "Roteamento"
  - `[low]` `[reject]` Passo numerado `0.` — cosmético; renumerar mexeria nas referências a passos sem ganho

## Design Notes

Formato da seção, depois de "Provedores novos":

```markdown
## Roteamento

- config: início <sha256> / fim <sha256>
- state_file: início <sha256> / fim <sha256>
- usage_file: início não alcançável / fim não alcançável
- roteamento inalterado: sim
```

A prova de "roteamento idêntico" tem três camadas (decisão do épico): hashes na proposta, este teste e a config só para leitura. O verify real no Orca é do Eduardo (`hitl`).

## Verification

**Commands:**
- `go test ./... && go vet ./...` -- expected: sem erro, incluindo o teste novo
- `printf 'package main\n// catalog-proposals\n' > zz_probe.go && go test -run TestProxyDoesNotReferenceCatalogProposals ./...; rm zz_probe.go` -- expected: o teste falha apontando `zz_probe.go`; o arquivo é removido depois
- `git diff --stat main -- . ':!_bmad-output'` -- expected: só `catalog_guard_test.go` e `docs/catalog-agent.md`

**Manual checks (if no CLI):**
- `docs/catalog-agent.md` cobre cada linha da matriz; o verify real (rodar no Orca, mudar a config no meio da rodada) é do Eduardo (`hitl`).

## Auto Run Result

**Summary:** Guarda de roteamento do agente de catálogo: hashes SHA-256 da config, do `state_file` e do `usage_file` no início e no fim da rodada, seção "Roteamento" com "roteamento inalterado: sim/não" (só pela config), teste em Go de que o proxy não cita `catalog-proposals`, e a config só para leitura documentada para o Orca.

**Files changed:**
- `catalog_guard_test.go` — novo; `TestProxyDoesNotReferenceCatalogProposals`
- `docs/catalog-agent.md` — passos 0 e 6, seção "Roteamento", bordas, passo do Eduardo no Orca

**Review:** 4 patches aplicados (medium 2, low 2), 0 adiados, 1 rejeitado (low: numeração `0.` do passo, cosmético).

**Follow-up review recommended:** false (nenhum `high`; 2 `medium` em texto de doc, já corrigidos).

**Verification:** `go test ./... && go vet ./...` ok; sonda `zz_probe.go` com `catalog-proposals` fez o teste falhar e foi removida. O verify real (rodar no Orca e mudar a config no meio da rodada) é do Eduardo (`hitl`) e não foi feito.

**Residual risks:** o documento nunca foi executado por um agente; a matriz de bordas das linhas de hash é coberta só pela doc, sem teste automatizado possível.

## Code Review

### 2026-10-30
**Code review complete.** 0 `decision-needed`, 5 `patch`, 1 `defer`, 11 rejected.

- [x] [Review][Patch] O teste de guarda passa sem varrer nenhum arquivo; contar os `.go` varridos e falhar se for 0 [catalog_guard_test.go:12-45]
- [x] [Review][Patch] A doc manda calcular SHA-256 sem nomear ferramenta nem fallback; nomear `shasum -a 256` (ou `sha256sum`) e, sem ferramenta, `não alcançável`, nunca um hash inventado [docs/catalog-agent.md passos 0 e 6]
- [x] [Review][Patch] A linha da tabela de bordas diz `não alcançável` nos dois lados, mas o passo 6 diz só no lado inacessível; alinhar [docs/catalog-agent.md:181 vs :125]
- [x] [Review][Patch] "roteamento inalterado: sim" só compara hash de início e fim (A→B→A passa; o passo 0 e a leitura do passo 1 são separados); dizer isso na doc [docs/catalog-agent.md:125-128]
- [x] [Review][Patch] Sem linha de borda para falha ao anexar a seção "Roteamento" no passo 6 [docs/catalog-agent.md]
- [x] [Review][Defer] Plano com `status: built` mas tarefas desmarcadas e Implementation Notes vazias — resolvido ao fechar: status done e tarefas marcadas

**Rejected:** casar só a string literal (inerente à checagem em texto, o ticket pede a citação literal); "não alcançável" agrupa vazio/ausente/permissão (simplificação deliberada); o teste pula dirs `_`/`.` (o Go ignora esses diretórios); linha de 1 MiB no Scanner (falha alta, arquivo gerado raro); caminho relativo e caminho só com espaços (raros, exigem regra nova); hash de arquivo meio escrito (informativo por desenho); código com span partido (grep mostra os spans inteiros).
