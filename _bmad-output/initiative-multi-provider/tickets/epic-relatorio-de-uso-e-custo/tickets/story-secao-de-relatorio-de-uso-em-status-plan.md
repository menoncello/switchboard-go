---
title: 'Seção de relatório de uso em /status'
type: 'feature'
ticket: 4
created: '2026-10-29'
status: built
baseline_revision: '56c0cc18d74bba1b2fef3d016d7ec2f21044edc2'
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

**Problem:** O histórico de uso (3.3) existe, mas nada o mostra: o Eduardo não vê quanto cada projeto, modelo, chave e tier usou (R13).

**Approach:** `/status` ganha, abaixo dos tiers, uma seção "Uso" renderizada no servidor a partir de `App.history`: o total do dia no topo, com o fuso, e uma tabela por dimensão (dia, projeto, modelo, chave, tier) dos últimos 7 dias dentro de `<details>`, em tokens e requests. Sem API nova.

## Boundaries & Constraints

**Always:** os 7 dias contam o dia de hoje no fuso `timezone` do histórico mais os 6 anteriores; projeto vazio aparece como "sem projeto" (`noProject`); a chave aparece só pelo nome já gravado; o `html/template` escapa modelo e projeto (vêm do cliente); as seções de chaves e tiers não mudam.

**Never:** preço, custo em dinheiro, teto, alerta SMTP (3.5 em diante); endpoint JSON novo; valor de chave, URL ou chave de tier no corpo; mudar o formato do `usage_file`.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Dois projetos | 2 requests com `X-Project` a e b | tabela por projeto com 2 linhas separadas | — |
| Total do dia | requests de hoje e de ontem | topo soma só hoje: requests, tokens in, out, cache; fuso (`UTC`, `America/Sao_Paulo`) ao lado | — |
| Fora da janela | linha com 8 dias | não entra em nenhuma tabela nem no total | — |
| Sem projeto | linha com projeto vazio ou `sem projeto` | linha "sem projeto" | — |
| Sem requests | histórico vazio na janela | a seção `Uso` não é renderizada | — |
| Valor hostil | modelo `<script>x</script>` | escapado | — |
| Sem usage | linhas com `no_usage` | coluna "sem usage" mostra a contagem | — |

</intent-contract>

## Code Map

- `adminkeys.go` -- `statusTmpl` (l.211-236, termina com a tabela de tiers e `</body>`), `statusPage` (l.245, embute `KeysReportResponse` e `Tiers`), `tierRows`, `handleStatusPage` (l.278). A seção entra depois de `{{end}}` dos tiers, antes de `</body>`; CSS inline na `<style>`.
- `history.go` -- `usageHistory` (`loc`, `now`, `snapshot()`, `usageRow` = `usageKey` + `usageCounts`). Adicionar um método que devolve hoje (`YYYY-MM-DD` no `loc`) e as linhas dos últimos 7 dias; não alterar gravação nem formato.
- `proxy.go` -- `noProject` (l.43).
- `main_test.go` -- `getStatusPage` (l.1205), `TestStatusPageTiersSection` (l.1696, padrão de vazamento de segredo), `TestAdminUnchangedWithTiers` (l.1676) compara `/status` de app sem e com tiers: com histórico vazio a seção some, então continua passando.
- `history_test.go` -- `historyApp`, `post(app, project)`, `app.history.now` para fixar a hora.
- `README.md` l.154 e `docs/configuration.md` -- descrição de `/status`.

## Tasks & Acceptance

**Execution:**
- [ ] `history.go` -- `window(days int)`: devolve `today string` e as linhas com `Day` entre hoje menos `days-1` e hoje, usando `h.now` e `h.loc`, sob `mu` -- única fonte da janela
- [ ] `adminkeys.go` -- tipo `usageReport` (fuso, hoje, totais do dia, e uma lista de linhas `{Label, Requests, TokensIn, TokensOut, TokensCacheIn, NoUsage}` por dimensão, na ordem dia (mais novo primeiro), projeto, modelo, chave, tier; as demais por requests decrescente e rótulo); campo `Usage *usageReport` em `statusPage`, nil quando não há linha na janela; template com `<h2>Uso</h2>`, total do dia com o fuso, e um `<details><summary>` com uma tabela por dimensão -- projeto vazio vira `noProject`
- [ ] `history_test.go` -- testes da matriz: dois projetos viram linhas separadas; total do dia soma só hoje e traz o fuso; linha de 8 dias fica fora; projeto vazio vira "sem projeto"; seção ausente sem requests e abaixo de `<h2>Tiers</h2>` quando há; modelo hostil escapado; corpo sem valor de chave nem de tier (padrão `SEGREDO` de `TestStatusPageTiersSection`)
- [ ] `README.md`, `docs/configuration.md` -- `/status` mostra a seção de uso (7 dias, por dimensão, só tokens e requests, o fuso de `timezone`)

**Acceptance Criteria:**
- Given duas requests com `X-Project` diferentes, when `GET /status`, then a tabela por projeto tem uma linha para cada.
- Given requests de hoje e de ontem, when `GET /status`, then o total do dia soma só hoje e mostra o fuso configurado.
- Given o proxy com tiers e uso, when `GET /status`, then `<h2>Uso</h2>` vem depois de `<h2>Tiers</h2>`.
- Given chaves e tiers com valores secretos, when `GET /status`, then o corpo não contém nenhum deles.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 5 findings — high 0, medium 0, low 5, false 0, maybe-false 0
- findings:
  - `[low]` `[reject]` `window` copia e ordena todas as linhas sob `h.mu` numa rota aberta — no máximo 90 dias de linhas e a cópia é rápida; o conserto mexe no lock do histórico.
  - `[low]` `[patch]` nota de segurança de `docs/operations.md` não citava o uso por projeto e modelo — nota atualizada.
  - `[low]` `[patch]` borda da janela (hoje-6 dentro, hoje-7 fora) sem teste — `TestStatusUsageWindowAndOrder`.
  - `[low]` `[patch]` tabelas de chave e tier, ordenação e cache no total do dia sem teste — mesmo teste.
  - `[low]` `[patch]` linha longa no README — reescrita em ~120 colunas.

## Design Notes

Tabela por dimensão = mesma agregação com a chave trocada: uma função recebe o seletor da dimensão (`func(usageRow) string`) e soma num mapa. Sem preço, os números são só tokens e requests; custo é de 3.5.

## Verification

**Commands:**
- `go vet ./...` -- expected: sem aviso
- `go test -race ./...` -- expected: verde, com os testes novos

## Auto Run Result

**Resumo:** `/status` ganhou a seção "Uso" abaixo dos tiers: total do dia com o fuso e, em `<details>`, uma tabela por dimensão (dia, projeto, modelo, chave, tier) dos últimos 7 dias, em tokens e requests. Projeto vazio aparece como "sem projeto"; a seção some sem requests na janela.

**Arquivos:**
- `history.go` -- `window(days)`
- `adminkeys.go` -- `usageReport`, template da seção
- `history_test.go` -- testes da matriz, da borda da janela e da ordenação
- `README.md`, `docs/configuration.md`, `docs/operations.md` -- descrição de `/status` e nota de segurança

**Revisão:** 5 achados (todos low), 4 patches aplicados (nota de segurança, testes de borda e de ordenação, README), 0 adiados, 1 rejeitado: custo do `window` sob o mutex (histórico limitado a 90 dias).
**Follow-up recomendado:** false.
**Verificação:** `go vet ./...` e `go test -race -count=1 ./...` verdes. Não abri `/status` no navegador; o layout só foi checado pelo HTML nos testes.
**Risco residual:** `/status` é aberto e agora mostra projeto e modelo vindos do cliente (documentado).
