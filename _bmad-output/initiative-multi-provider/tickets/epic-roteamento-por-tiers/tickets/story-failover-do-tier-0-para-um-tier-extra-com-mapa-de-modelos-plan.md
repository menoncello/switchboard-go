---
title: "Failover do tier 0 para um tier extra com mapa de modelos"
type: 'feature'
ticket: 2
created: '2026-10-29'
status: done
baseline_revision: '09cdd29eaf150055a6dd07252056f3a985f8a761'
route: 'full'
route_source: 'auto'
risk: 'high'
review: 'quick'
review_source: 'pinned'
lenses_ran: [quick]
review_loop_iteration: 0
followup_review_recommended: true
context: []
warnings: []
deferred:
  - summary: >-
      O aviso SMTP "all exhausted" não sai enquanto um tier atende a request.
    evidence: |-
      proxyV1 só chama NotifyAllExhausted depois de proxyTiers falhar; antes desta story o aviso saía quando as chaves do tier 0 acabavam. Falta decidir se a troca para um tier também deve avisar (R16 cobre só o caso sem tier).
    location: >-
      proxy.go:proxyV1
    severity: medium (unverified)
---

<intent-contract>

## Intent

**Problem:** Com todas as chaves do tier 0 (OpenCode Go) esgotadas, `proxyV1` devolve 429 local mesmo havendo `tiers:` na config; os tiers lidos pela story 11 ainda não são usados.

**Approach:** Esgotado o tier 0, `proxyV1` tenta os tiers da config em ordem, reescrevendo o campo `model` do corpo pelo mapa do tier; tier sem mapeamento para o modelo, ou que não fala o estilo do request, é pulado. Request sem `model` no corpo fica no tier 0.

## Boundaries & Constraints

**Always:** o URL e a `PROXY_API_KEY` dos agentes não mudam; o tier 0 continua girando chaves como hoje (429 de cota e 403 de assinatura); `/admin/*`, `/status` e `/admin/keys` devolvem o mesmo de antes; o 429 local de "all upstream keys exhausted" (com `Retry-After` e avisos SMTP) continua quando nenhum tier atende; demais campos do corpo passam intactos; ID do modelo no tier = `TierModel.ID`, ou `Name` quando `ID` vazio.

**Never:** cooldown, persistência, log estruturado, aviso Telegram, failover por 5xx/401/402/404 ou por erro de conexão, checagem de `min_context`/`tools`, seção de tiers na página de status (stories seguintes); tradução entre estilos de API; mexer em `config.go`.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Failover | tier 0 com 429 de cota em todas as chaves; `model: "a"` mapeado no tier 1 | tier 1 recebe a request com `model` = id mapeado, `Authorization: Bearer <api_key do tier>`; resposta do tier 1 volta ao cliente | No error expected |
| Sem mapeamento | idem, `model: "z"` não mapeado em nenhum tier | 429 local "all upstream keys exhausted" com `Retry-After`; nenhum tier chamado | 429 local |
| Sem model | `GET /v1/models` ou corpo sem `model`/não JSON, tier 0 esgotado | fica no tier 0: 429 local, nenhum tier chamado | 429 local |
| Estilo | request Anthropic, tier só com estilo OpenAI | tier pulado | 429 local se nenhum outro atende |
| Tier também esgotado | tier 1 devolve 429 de cota | passa ao tier seguinte; se nenhum atende, 429 local | 429 local |
| Tier 0 saudável | tier 0 responde 200 | tier 1 nunca é chamado | No error expected |
| Estilo Anthropic no tier | request Anthropic, tier com `anthropic` | tier recebe `x-api-key` com a chave do tier e `anthropic-version` | No error expected |

</intent-contract>

## Code Map

- `proxy.go` -- `proxyV1` (loop de chaves do tier 0, 429 local ao final) e `doUpstream` (monta URL a partir de `a.config.UpstreamBaseURL`, define headers por estilo); `isQuota429` reusável para detectar tier esgotado. Chamadores de `doUpstream`: `rg "doUpstream\("` (inclui testes em `main_test.go`).
- `config.go` -- `TierConfig{Name, BaseURL, APIKey, Free, Styles, Models}` e `TierModel{Name, ID, Context, Tools}`, já lidos e validados; só ler, não alterar.
- `server.go` -- `App`, `newApp`; sem mudança prevista.
- `main_test.go` -- padrão de teste: `httptest.NewServer` + `newApp(Config{...})` + `doProxyReq(app)`; acrescentar os testes novos aqui.
- `docs/configuration.md` -- onde `tiers:` está documentado; acrescentar o comportamento de failover.
- `cmd/fakeupstream/main.go` -- novo, upstream falso para teste manual.

## Tasks & Acceptance

**Execution:**
- [x] `proxy.go` -- dar a `doUpstream` o `baseURL` como parâmetro (tier 0 passa `a.config.UpstreamBaseURL`); extrair a escolha de modelo (ler `model` do corpo JSON de topo; vazio ⇒ sem failover) e a reescrita do corpo (`map[string]json.RawMessage`, só troca `model`); após o loop do tier 0, antes do 429 local, percorrer `a.config.Tiers` em ordem: pular se não fala o estilo ou sem mapeamento; chamar com `APIKey` do tier; resposta com `isQuota429` fecha e segue; erro de conexão do tier devolve 502 ao cliente, sem seguir ao próximo tier; qualquer outra resposta é copiada ao cliente -- failover do tier 0
- [x] `main_test.go` -- testes da matriz: failover com model reescrito e chave do tier, modelo sem mapa, sem model, estilo, tier também esgotado, tier 0 saudável (tier 1 não chamado), tier Anthropic; ajustar chamadas existentes de `doUpstream`; confirmar que `/admin/keys`, `/admin/status` e `/status` seguem iguais com tiers configurados -- cobrir a matriz
- [x] `cmd/fakeupstream/main.go` -- servidor HTTP mínimo (`-addr`, padrão `127.0.0.1:9999`): devolve 429 de cota (`{"error":{"code":"insufficient_quota","message":"quota exceeded"}}`) quando em modo quota e 200 JSON com o `model` recebido quando em modo ok; `POST /__mode/quota` e `POST /__mode/ok` alternam; modo inicial `ok` -- upstream falso do critério de verificação
- [x] `docs/configuration.md` -- um parágrafo: tier 0 esgotado ⇒ tiers em ordem, reescrita de `model`, tier sem mapeamento ou sem o estilo é pulado, request sem `model` fica no tier 0; como rodar `go run ./cmd/fakeupstream` -- documentação

**Acceptance Criteria:**
- Given dois upstreams httptest, o do tier 0 devolvendo 429 de cota em todas as chaves, when chega request com modelo mapeado, then o tier 1 a atende com o `model` reescrito e o cliente recebe a resposta dele.
- Given o mesmo cenário, when chega request com modelo sem mapeamento, then o cliente recebe o 429 local de todas esgotadas.
- Given tiers configurados, when `/admin/keys`, `/admin/status` e `/status` são chamados, then devolvem o mesmo de antes da mudança.

## Implementation Notes

**Decisions**
- `doUpstream` agora recebe `baseURL` (antes de `key`) e faz `TrimRight(baseURL, "/")`.
- Failover em `proxyTiers` (proxy.go), chamado depois do loop do tier 0. Tier 0 continua devolvendo 502 em erro de conexão, como antes.
- O modelo do cliente é casado com `TierModel.Name`; o corpo sai com `TierModel.ID` (ou `Name` se `ID` vazio).
- Aviso SMTP "all exhausted" e `Retry-After` só rodam quando nenhum tier atende (ficam depois do failover).
- Resposta de tier que não seja 429 de cota (inclusive 4xx/5xx) é copiada ao cliente; só o 429 de cota passa ao próximo tier. Erro de conexão no tier devolve 502 (corrigido no review; failover por conexão é da story 12).

**Arquivos tocados**
- `proxy.go`, `main_test.go` (testes novos + chamadas de `doUpstream` ajustadas), `docs/configuration.md`, `cmd/fakeupstream/main.go` (novo).

**Surpresas**
- `config.go` já preenche `ID` com o nome quando vazio; o fallback em `tierModelID` é só defensivo para `Config` montada à mão.
- Verificação: `go build`, `go vet`, `go test ./...` passam. Roteiro manual com `fakeupstream` e curl rodado no review: modelo mapeado volta 200 do tier 1 com `model` reescrito; modelo sem mapa volta o 429 local.

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 10 findings — high 1, medium 4, low 4, false 0, maybe-false 1
- findings:
  - `[high]` `patch` Dockerfile `go build -o ... ./...` quebra com o segundo `main` — confirmado com `go build -o /tmp/x ./...`; alvo trocado para `.`
  - `[medium]` `patch` erro de conexão do tier seguia ao próximo e cancelamento do cliente continuava o loop, contra o Never do plano — agora devolve 502
  - `[low]` `patch` `ctx` como segundo parâmetro de `proxyTiers` — movido para o primeiro
  - `[medium]` `patch` teste de failover não checava o corpo devolvido ao cliente — asserção acrescentada
  - `[medium]` `patch` faltavam testes: 4xx/5xx de tier copiado, `ID` vazio, erro de conexão — três testes novos
  - `[low]` `patch` goroutines dos handlers de teste sem sincronização — mutex e leituras atômicas; `go test -race ./...` passa
  - `[low]` `reject` checkboxes do plano e roteiro manual pendentes — fix seria editar o plano; o roteiro manual foi rodado no review
  - `[medium]` `patch` docs omitiam resposta não-429 de tier, 502 e alerta SMTP — parágrafo ampliado
  - `[medium]` `defer` o aviso SMTP "all exhausted" não sai enquanto um tier atende — decisão do plano, mas muda o comportamento anterior; falta confirmar se o Eduardo quer aviso também na troca para o tier
  - `[low]` `reject` `json.Marshal` reordena chaves e escapa `<>&` no corpo — o JSON é semanticamente idêntico; corrigir pediria um reescritor de bytes

## Design Notes

Reescrever só o campo `model` com `map[string]json.RawMessage` mantém os valores dos demais campos, mas o corpo é recodificado: espaços compactados, chaves reordenadas e `<>&` escapados (números ficam como estão). Corpo que não é objeto JSON, ou sem `model` string não vazia, vale como "sem model".

## Verification

**Commands:**
- `go build ./... && go vet ./...` -- expected: sem erros
- `go test ./...` -- expected: todos passam, incluindo os testes novos
- `go run ./cmd/fakeupstream -addr 127.0.0.1:9999 &` e curl em `POST /__mode/quota` -- expected: 429 de cota no fake; com o proxy apontando tier 0 e tier 1 para fakes, um curl a `/v1/chat/completions` volta do tier 1

## Auto Run Result

- Mudança: com o tier 0 esgotado, `proxyV1` passa pelos `tiers:` em ordem, reescreve o `model` pelo mapa e usa a chave do tier; tier sem mapa ou sem o estilo é pulado, 429 de cota passa ao próximo, request sem `model` fica no tier 0.
- Arquivos: `proxy.go` (`proxyTiers`, `doUpstream` com `baseURL`), `main_test.go` (testes da matriz), `cmd/fakeupstream/main.go` (upstream falso), `docs/configuration.md`, `Dockerfile` (alvo `.`).
- Review: 10 achados; 7 corrigidos, 1 adiado (aviso SMTP na troca de tier), 2 rejeitados (checkboxes do plano; reordenação de chaves do JSON).
- Follow-up recomendado: sim, porque 1 patch `high` (Dockerfile) e 4 `medium` foram aplicados sem nova revisão do código corrigido, em especial o 502 de erro de conexão no tier.
- Verificação: `go build`, `go vet`, `go test -race ./...` passam; roteiro manual com dois fakes e o proxy: mapeado 200 do tier 1, não mapeado 429 local.
- Risco residual: o aviso por e-mail não sai mais enquanto um tier atende.

## Code Review

### 2026-10-29
Revisão do ticket 2.2 (4 lentes): 1 decision-needed, 9 patch, 0 defer, 15 rejeitados.

- [x] [Review][Decision] Sem aviso SMTP nem log quando o tier 0 esgota e um tier atende — o failover mascara a exaustão e o operador só sabe quando todos falham. Options: (a) chamar `NotifyAllExhausted` também na troca para um tier; (b) só uma linha de log na troca; (c) manter como está e só documentar. Recommended: (b) — barato e cabe no "sem log estruturado" do plano; o alerta por e-mail na troca é decisão de escopo do Eduardo.
- [x] [Review][Patch] Sem teste de que `Retry-After` não sai e `ShouldNotifyAllExhausted` segue armado quando um tier atende [main_test.go:TestTierFailoverRewritesModelAndKey]
- [x] [Review][Patch] Sem teste de que um 429 de tier sem marca de cota é copiado ao cliente, sem tentar o tier seguinte [main_test.go:TestTierNonQuotaResponseCopiedWithoutFurtherFailover]
- [x] [Review][Patch] Sem teste de `base_url` de tier com barra final e prefixo de path (`/api/v1/`) [main_test.go]
- [x] [Review][Patch] Erro de conexão do tier devolve `err.Error()` com a URL do tier no corpo do 502 [proxy.go:proxyTiers]
- [x] [Review][Patch] Seção Tasks do plano ainda diz "erro de conexão do tier segue ao próximo" [plan:Tasks & Acceptance]
- [x] [Review][Patch] Design Notes promete corpo "byte a byte"; `json.Marshal` compacta e escapa `<>&` [plan:Design Notes]
- [x] [Review][Patch] `TestTierNoMappingOrNoModelStaysLocal429` depende da ordem dos casos para o `hits == {1,0,0}` [main_test.go:1501]
- [x] [Review][Patch] `tierModelID`: o laço `speaks` vira `slices.Contains(t.Styles, style)` [proxy.go:tierModelID]
- [x] [Review][Patch] `fakeupstream`: `GET /__mode/quota` cai no catch-all e devolve 200 como se tivesse funcionado [cmd/fakeupstream/main.go]

**Rejected**
- Failover em 401/403/5xx de tier, e conexão não seguir ao próximo tier — falso: proibido pela lista Never do plano e documentado.
- Outros caminhos de build com `./...` — falso: README não usa `-o`, goreleaser usa `main: .`.
- Nomes de modelo duplicados sem validação — falso: `config.go` rejeita.
- Match de modelo sem case/trim — falso: casar exato com o `Name` configurado é o desenho.
- Lacunas de docs — falso: o parágrafo já cobre qualquer status, 502 e SMTP.
- Caminho curl sem teste automático — falso: verificação manual por desenho, rodada no review.
- Dockerfile fora do escopo — falso: correção necessária, justificada.
- Teste admin/status fora de estado de failover; um só tier 0 nos testes; `TestDoUpstream*` com mesma URL — baixo, endpoints não usam tiers, rotação é anterior, tier tests cobrem.

Decisão do aviso na troca de tier: opção (b), log na troca, exibido na tela de acompanhamento. Fica para a story da seção de tiers na página de status, pois o Never desta story proíbe mexer em `/status`.
