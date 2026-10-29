---
title: 'Config de tiers: leitura, validação e documentação'
type: 'feature'
ticket: '11'
created: '2026-10-29'
status: done
baseline_revision: 'b1dae98dd2a50c39298a87c5775b939595774e00'
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

**Problem:** O proxy só conhece o OpenCode Go (`upstream:`); o failover das próximas stories precisa de uma lista `tiers:` na config, lida e validada na partida.

**Approach:** Ler `tiers:` do YAML para `Config.Tiers` e validar em `validateConfig`; sem comportamento novo no proxy. A config atual segue como tier 0 implícito. Documentar em `docs/configuration.md`.

## Boundaries & Constraints

**Always:** Cada tier tem `name`, `base_url`, `api_key`, `free` e `models`, um mapa nome-do-modelo → objeto (`id`, `context` e `tools` opcionais); `styles` (lista de `openai`/`anthropic`) ausente vale só OpenAI. Tier sem `free: true` é rejeitado; o tier 0 implícito é isento. `safeConfigSummary` e `validate-config` nunca imprimem `api_key`; mostram só a contagem de tiers. Config sem `tiers:` se comporta como hoje.

**Never:** Mudar `proxyV1`, `KeyManager` ou `adminkeys.go`; `tiers:` por variável de ambiente; `cooldown`, `min_context` e `state_file` (stories seguintes); validar a URL do tier além de não vazia.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Dois tiers gratuitos | `tiers:` com 2 itens `free: true`, modelos e `base_url` | `loadConfig` ok; `Config.Tiers` com 2 itens, `styles` ausente = OpenAI | No error expected |
| Sem `tiers:` | YAML sem a chave | `Tiers` vazio, comportamento de hoje | No error expected |
| Tier pago | `free` ausente ou `false` | `validateConfig` rejeita citando o tier | Erro na partida |
| Campo faltando | `base_url` ou `api_key` ou `name` vazio | rejeitado | Erro na partida |
| Modelo repetido | mesma chave duas vezes no mapa `models` | rejeitado por `validateConfig` | Erro na partida |
| Estilo desconhecido | `styles: [grpc]` | rejeitado | Erro na partida |

</intent-contract>

## Code Map

- `config.go:15-42` -- `Config`; acrescentar `Tiers []TierConfig` (e os tipos `TierConfig`, `TierModel`).
- `config.go:96-120` -- `yamlConfig`; acrescentar `Tiers []yamlTier `yaml:"tiers"``.
- `config.go:122-156` -- `loadYAMLConfig` monta o `Config` numa linha; converter `yc.Tiers` e devolver em `Tiers`.
- `config.go:158-200` -- `mergeConfig`; copiar `Tiers` quando `len(src.Tiers) > 0`.
- `config.go:294-321` -- `validateConfig` e `safeConfigSummary`; validar tiers, acrescentar `tiers=N` ao resumo.
- `server.go:133,226`, `main.go:15-23` -- só chamam `validateConfig`/`safeConfigSummary`; não tocar.
- `main_test.go:330` -- testes de `validateConfig`; seguir o padrão (config mínima válida + um campo ruim). `yaml.v3` já está no `go.mod`.
- `docs/configuration.md` -- exemplo YAML (linha 42) e seções; acrescentar `tiers:`.

## Tasks & Acceptance

**Execution:**
- [x] `config.go` -- tipos `TierConfig{Name, BaseURL, APIKey string; Free bool; Styles []APIStyle; Models []TierModel}` e `TierModel{Name, ID string; Context int; Tools bool}`; `yamlTier` com `models` como `yaml.Node` decodificado em pares na ordem, para a chave repetida chegar à validação em vez de falhar no parse; `styles` (strings `openai`/`anthropic` no YAML) vira `[]APIStyle` com as constantes `APIStyleOpenAI`/`APIStyleAnthropic` de `proxy.go`; ausente vira `[]APIStyle{APIStyleOpenAI}`; ligar em `loadYAMLConfig` e `mergeConfig` -- leitura
- [x] `config.go` -- `validateConfig`: para cada tier, `name` não vazio e único, `base_url` e `api_key` não vazios, `free` verdadeiro, ao menos um modelo, nomes de modelo únicos e não vazios, `context >= 0`, estilo conhecido; mensagens citam o tier pelo `name` e nunca o `api_key` -- validação
- [x] `config.go` -- `safeConfigSummary`: acrescentar `tiers=N` -- sem vazar chave
- [x] `main_test.go` -- testes: dois tiers gratuitos carregam via YAML (`SWITCHBOARD_GO_CONFIG` com `t.TempDir`); cada rejeição da matriz; resumo não contém o `api_key`; config sem `tiers:` igual à de hoje -- cobertura da matriz
- [x] `docs/configuration.md` -- `tiers:` no exemplo YAML e uma seção descrevendo campos, `free` obrigatório, `styles` padrão OpenAI, formato do mapa de modelos e tier 0 implícito -- documentação

**Acceptance Criteria:**
- Given um YAML com dois tiers gratuitos, when `loadConfig` roda, then carrega sem erro com `len(Tiers) == 2`.
- Given um tier sem `free: true`, sem `base_url`, sem `api_key` ou com modelo repetido, when `validateConfig` roda, then retorna erro.
- Given qualquer config com tiers, when `safeConfigSummary` roda, then a saída não contém nenhum `api_key`.
- Given uma config sem `tiers:`, when `go test ./...` roda, then os testes existentes passam sem alteração.

## Implementation Notes

Rota full: ~150 linhas entre `config.go`, testes e docs. O mapa `models` vira slice ordenada porque um `map` do Go não carrega chave repetida e o `yaml.v3` falharia no parse antes de `validateConfig`.

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 5 findings — high 0, medium 0, low 5, false 0, maybe-false 0
- findings:
  - `[low]` `[reject]` Unicidade de nome de tier/modelo usa a string crua, enquanto o vazio usa TrimSpace — nomes que só diferem por espaço passam; improvável em config escrita à mão, e o fix adiciona normalização.
  - `[low]` `[reject]` `models:` nulo, alias ou merge key dá erro de parse genérico ou vira modelo `<<` — caso de borda raro; o plano só exige que a chave repetida chegue à validação.
  - `[low]` `[reject]` Estilo desconhecido não é citado na mensagem; `styles` duplicado ou `[]` são aceitos — o plano não pede mais que "estilo conhecido" e "ausente vale OpenAI".
  - `[low]` `[patch]` `TestLoadConfigTiersRejected` só checava `err != nil` e um erro de parse mascararia `dup model` — cada caso agora confere a mensagem esperada; testes passam, então cada regra dispara por si.
  - `[low]` `[reject]` Casos de rejeição passam por `loadConfig` em vez de `validateConfig` direto — exercita o caminho real da partida e cobre a mesma validação.

## Verification

**Commands:**
- `gofmt -l .` -- expected: sem saída
- `go build ./... && go vet ./... && go test ./...` -- expected: sucesso
- `git diff --stat -- proxy.go keys.go adminkeys.go` -- expected: vazio

## Auto Run Result

- Summary: `tiers:` lido do YAML em `Config.Tiers` e validado em `validateConfig`; a config atual segue como tier 0 implícito; sem comportamento novo no proxy.
- Files: `config.go` (tipos, leitura, merge, `validateTiers`, `tiers=N` no resumo), `main_test.go` (3 testes), `docs/configuration.md` (exemplo e seção Tiers).
- Review: quick, 5 achados (todos low); 1 patch (testes de rejeição conferem a mensagem), 4 rejeitados (bordas improváveis de nome com espaço e `models` nulo/alias, estilo duplicado ou sem citação na mensagem, testes via `loadConfig`), 0 adiados.
- Follow-up review recommended: false (1 patch low).
- Verification: `gofmt -l .` vazio; `go build`, `go vet`, `go test` passam; `proxy.go`, `keys.go` e `adminkeys.go` intactos.
- Residual risks: `models` que não é mapa falha no parse com erro genérico.

## Code Review

### 2026-10-29
Code review complete. 0 `decision-needed`, 4 `patch`, 0 `defer`, 22 rejected.

- [x] [Review][Patch] `id` do modelo é opcional (decisão: opção b): usar o nome do modelo como padrão em convertTiers e corrigir a doc em docs/configuration.md [config.go:convertTiers]
- [x] [Review][Patch] TestLoadConfigTiersRejected não compara `want` com o erro — cada caso pode passar por outro motivo [main_test.go:TestLoadConfigTiersRejected]
- [x] [Review][Patch] Faltam casos para valor de modelo que não é objeto (`m1: 5`) e `models` que não é mapa — os ramos de erro de convertTiers não são exercitados [main_test.go:TestLoadConfigTiersRejected]
- [x] [Review][Patch] Nomes de tier "a" e "a " passam como distintos: a checagem de duplicidade usa o nome sem TrimSpace [config.go:validateTiers]

Rejected:
- R3, R12 (base_url só não vazia): fora de escopo, o plan veta validar a URL além de não vazia.
- R15 (indentação das fixtures): válida, as linhas anexadas são irmãs de `models`.
- R16 (`tiers: []` não limpa): só o arquivo YAML define tiers.
- R21 (testes chamam loadConfig): loadConfig termina em validateConfig.
- R22 (substring `secret-`): as duas chaves de teste começam com `secret-`.
- R24 (vazamento de api_key em erro do YAML): erro de tipo do yaml.v3 não imprime o valor de `api_key`, que aceita qualquer escalar.
- R25 (`tiers=0` no resumo): doc e plan dizem que o resumo mostra a contagem.
- R4, R5 (null/merge key/estilos), R6, R7, R9, R10, R13, R23, R26: low, raros e a correção acrescenta código; a chave de merge e o `models:` nulo ficam cobertos pelo F1 ou ainda são rejeitados no load.
