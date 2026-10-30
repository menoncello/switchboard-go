---
title: 'Histórico de uso persistido em usage_file'
type: 'feature'
ticket: 3
created: '2026-10-29'
status: built
baseline_revision: '778f6252dae1b7cf35ca74894564096c5143a366'
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

**Problem:** O proxy loga tokens por request (3.2), mas não soma nada: sem um histórico por dia, projeto, modelo, chave e tier, que sobreviva a reinício, não há relatório (R13) nem gasto do dia (R10).

**Approach:** Um histórico em memória, somado a cada request que um tier atendeu, gravado em `usage_file` (JSON, escrita atômica atrasada de poucos segundos e na saída), carregado na partida, com retenção de 90 dias e o dia calculado no fuso `timezone` (nome IANA, padrão UTC, tzdata embutido, inválido rejeitado em `validateConfig`). Sem `usage_file` o histórico fica só em memória e a partida avisa.

## Boundaries & Constraints

**Always:** a falha de escrita só é logada e nunca afeta a request; a escrita reusa `writeFileAtomic`; `usage_file` e `timezone` são só YAML, como `state_file`; o log por request e o `state_file` não mudam; uma linha agregada por (dia, projeto, modelo, chave, tier).

**Never:** relatório em `/status`, tabela de preços, teto diário e alerta SMTP (stories 3.4 em diante); persistir a cada request; gravar valor de chave (a chave entra só pelo nome de `key_name`, ou `key-<índice>` quando o nome é vazio; vazia nos tiers extras).

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Dois projetos | 2 requests de `X-Project` diferentes, mesmo modelo e tier | 2 linhas agregadas, cada uma com `requests=1` e os tokens da sua request | — |
| Mesma dimensão | 2 requests iguais | 1 linha com `requests=2` e tokens somados | — |
| Sem usage | resposta 2xx POST sem `usage` | `requests` conta, `no_usage` conta, tokens não somam | — |
| Resposta de tier não 2xx | 5xx guardada servida ao cliente | `requests` conta, sem `no_usage` nem tokens | — |
| Nenhum tier atendeu | 429 local, 502, 499, 413, 400 | nada é somado | — |
| Reinício | arquivo gravado, novo `App` com o mesmo `usage_file` | totais iguais | — |
| Dia antigo | linha com dia anterior a hoje menos 90 dias no arquivo | descartada ao carregar e ao gravar | — |
| Virada do dia | `now` 2026-10-30T01:00Z | dia `2026-10-29` com `America/Sao_Paulo`, `2026-10-30` em UTC | — |
| Timezone inválido | `timezone: "Marte/Olimpo"` | `validateConfig` devolve erro | rejeitado na partida |
| Sem `usage_file` | `usage_file` vazio | nada gravado; soma em memória; aviso na partida | — |
| Arquivo ausente ou inválido | na partida | começa vazio, loga | não derruba |
| Falha de escrita | diretório do `usage_file` inexistente | request responde normal; erro logado | só log |

</intent-contract>

## Code Map

- `usage.go` -- `usageTokens{in, out, cache int; found, partial bool}`, `parseUsage`; não alterar. O histórico novo vai em `history.go`.
- `proxy.go` -- `proxyV1` (l.98-129) monta o log: `tier`, `keyIdx`, `usage`, `rec.status`, `requestProject(r.Header)`, o teste `tier != "" && rec.status/100 == 2 && r.Method == http.MethodPost` que define `usage_missing`. A soma entra aqui, só quando `tier != ""`.
- `server.go` -- `App` (l.21), `newApp` (l.44, sem retorno de erro, chama `loadState`), `writeFileAtomic` (l.109, reusar), `saveState` (l.83, o padrão: erro só logado). `handleReadyz` chama `validateConfig`.
- `config.go` -- `Config` (l.51 `StateFile`), `yamlConfig` (l.158), `loadYAMLConfig` (l.268 monta `Config`), `mergeConfig` (l.290 copia `StateFile` se não vazio), `validateConfig` (l.422), `warnNoStateFile` (l.522, o modelo do novo aviso). `Config.keyName(idx)` dá o nome da chave.
- `main.go` -- `validate-config` e a partida chamam `warnNoStateFile`; o goroutine de `Shutdown` roda em paralelo e `ListenAndServe` volta antes das requests acabarem, então a gravação final precisa ficar depois do `Shutdown` e `main` esperar por ela.
- `main_test.go` -- `newApp(Config{...})` (~l.2003 `TestRequestLogProjectHeader`), `captureLog(app)` (l.1771), `tierReq` (l.1482), `newStub` (l.2064); `warnNoStateFile` testado em ~l.2848; `validateConfig` com config válida mínima em l.339.
- `docs/configuration.md` -- exemplo YAML (l.88 `state_file`) e seção "State file" (l.105, termina com "Daily spend is not persisted."). `docs/docker.md` l.42-58 monta o volume do `state_file`.

## Tasks & Acceptance

**Execution:**
- [ ] `history.go` (novo) -- tipo `usageHistory`: mapa de `usageKey{Day, Project, Model, Key, Tier}` para contagens `requests, tokens_in, tokens_out, tokens_cache_in, no_usage`; `loc *time.Location`, `now func() time.Time` (testes), `path`, mutex, `dirty`, timer. `record(project, model, key, tier string, u usageTokens, noUsage bool)` soma e agenda uma gravação 3 s depois (`time.AfterFunc`, um timer por vez). `flush()` poda dias anteriores a hoje menos 90 (comparação de string `YYYY-MM-DD`), serializa as linhas ordenadas em `{"rows":[...]}` e chama `writeFileAtomic`; em erro loga e mantém `dirty` para a próxima. `load()` na criação lê o arquivo, ignora ausente, loga inválido, e já descarta dias fora da retenção. `close()` para o timer e faz `flush()`. `snapshot()` devolve as linhas ordenadas (testes e a story do relatório). `path` vazio: só memória, `flush` não faz nada. Comentar `shortcut:` para o modelo vir do cliente sem limite de linhas
- [ ] `config.go` -- campos `UsageFile` e `Timezone` em `Config`, `yamlConfig` (`usage_file`, `timezone`) e `loadYAMLConfig`/`mergeConfig` (copiar se não vazio, como `StateFile`); `validateConfig` rejeita `Timezone` não vazio que `time.LoadLocation` não resolva; importar `_ "time/tzdata"` para o binário não depender do SO; `warnNoUsageFile(cfg)` no molde de `warnNoStateFile` (avisa que o histórico se perde ao reiniciar)
- [ ] `server.go` -- `App.history *usageHistory`, criado em `newApp` com o `loc` da config (vazio ou não resolvido cai em UTC) e o `UsageFile`
- [ ] `proxy.go` -- em `proxyV1`, quando `tier != ""`, chamar `a.history.record` com o projeto já calculado, o `model` truncado, a chave (`keyName`, ou `key-<idx>` se vazio, só quando `keyIdx >= 0`), o tier, os tokens e `noUsage` igual à condição de `usage_missing`
- [ ] `main.go` -- `warnNoUsageFile(cfg)` onde `warnNoStateFile` é chamado (validate-config e partida); o goroutine de shutdown chama `a.history.close()` depois de `srv.Shutdown` e fecha um canal `done`; `main` espera `done` depois que `ListenAndServe` volta com `ErrServerClosed`
- [ ] `main_test.go` -- testes da matriz: dois projetos viram duas linhas; mesma dimensão soma; sem usage e não 2xx; nenhum tier atendeu (429 local) não soma; reinício mantém totais (`flush` e novo `newApp` com o mesmo arquivo); dia antigo descartado; virada do dia por fuso com `now` fixo; `validateConfig` rejeita timezone inválido e aceita válido e vazio; sem `usage_file` nada é gravado (diretório temporário vazio) e `warnNoUsageFile` loga; falha de escrita (diretório inexistente) não afeta o status da request; a gravação atrasada ocorre sozinha (arquivo aparece sem chamar `flush`)
- [ ] `docs/configuration.md` -- `usage_file` e `timezone` no exemplo YAML e uma seção "Usage history" (formato do arquivo, agregação por dia, projeto, modelo, chave e tier, retenção de 90 dias, gravação atrasada de poucos segundos e na saída, fuso IANA com padrão UTC, o diretório precisa aceitar escrita, sem `usage_file` só em memória com aviso); ajustar a frase final da seção "State file" se ficar desatualizada
- [ ] `docs/docker.md` -- o volume também guarda `usage_file` (ex.: `/state/usage.json`), mesma regra de montar o diretório

**Acceptance Criteria:**
- Given duas requests com `X-Project` diferentes atendidas por um tier, when o histórico é lido, then há duas linhas agregadas com os tokens de cada uma.
- Given um `usage_file` gravado, when um novo `App` é criado com o mesmo arquivo, then os totais são os mesmos.
- Given um arquivo com um dia de mais de 90 dias, when o `App` carrega e depois grava, then esse dia não está no arquivo.
- Given `timezone: "America/Sao_Paulo"`, when a hora UTC passa da meia-noite mas o horário local não, then a request entra no dia local.
- Given `timezone` inválido na config, when `validateConfig` roda, then devolve erro.
- Given um servidor sem `usage_file`, when atende requests, then nenhum arquivo é criado e a partida e o `validate-config` logaram o aviso.
- Given uma falha de escrita do `usage_file`, when uma request termina, then o cliente recebe a resposta normal.
- Given um SIGTERM com requests recentes ainda sem gravar, when o proxy encerra, then o arquivo tem todas elas.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 8 findings — high 0, medium 0, low 8, false 0, maybe-false 0
- findings:
  - `[low]` `[patch]` `TestHistoryNoUsageFile` não apontava o app para o diretório checado — teste agora faz `Chdir` para o diretório temporário e confere que segue vazio.
  - `[low]` `[reject]` `flush` segura o mutex durante a escrita — arquivo pequeno em disco local; o conserto exige segundo mutex e cópia das linhas.
  - `[low]` `[reject]` escrita que falhou não é retentada com o proxy ocioso — tentada de novo na próxima soma e na saída; o conserto exige novo timer.
  - `[low]` `[reject]` request que termina depois do `close()` (stream além dos 10 s do `Shutdown`) não entra no arquivo — raro; o conserto exige estado de fechado e outro caminho de gravação.
  - `[low]` `[patch]` `timezone: "Local"` aceito por `time.LoadLocation` — `validateConfig` rejeita `Local`, com caso no teste.
  - `[low]` `[reject]` `snapshot()` sem poda no modo só memória — acúmulo desprezível; o relatório (3.4) filtra por data.
  - `[low]` `[reject]` timers de teste ficam armados — só ruído em teste, sem falha observada com `-race`.
  - `[low]` `[reject]` `usage_file` e `state_file` podem apontar o mesmo caminho — erro de configuração do operador; o conserto acrescenta validação cruzada.

## Design Notes

A gravação atrasada usa um único `time.AfterFunc` armado pela primeira soma depois da última gravação; somas seguintes só marcam `dirty`. Isso limita a escrita a uma por janela de 3 s sem goroutine permanente. `now` e o atraso ficam em campos para os testes.

Linha do arquivo (uma por dimensão): `{"day":"2026-10-29","project":"sem projeto","model":"glm-5.1","key":"main","tier":"opencode-go","requests":2,"tokens_in":120,"tokens_out":30,"tokens_cache_in":0,"no_usage":0}`.

`shortcut:` o modelo vem do cliente (cortado em 128 bytes) e as linhas não têm limite; um cliente autenticado pode inflar o arquivo, e a retenção de 90 dias é o único freio. Limitar quando houver mais de um cliente não confiável.

## Verification

**Commands:**
- `go vet ./...` -- expected: sem aviso
- `go test ./...` -- expected: verde, com os testes novos do histórico
- `go build ./... && go run . validate-config` -- expected: o aviso de `usage_file` ausente aparece com uma config válida sem `usage_file` (ignorar se faltar config de ambiente)

## Auto Run Result

**Resumo:** o proxy soma cada request atendida por um tier por dia, projeto, modelo, chave e tier (requests, tokens, requests sem usage), grava em `usage_file` (JSON atômico, 3 s depois da primeira soma e na saída), carrega na partida, descarta o que passa de 90 dias e calcula o dia no `timezone` (IANA, padrão UTC, tzdata embutido, inválido e `Local` rejeitados). Sem `usage_file` fica só em memória, com aviso na partida e no `validate-config`.

**Arquivos:**
- `history.go` -- `usageHistory`: soma, carga, poda, gravação atrasada, `close`
- `config.go` -- `UsageFile`, `Timezone`, validação, `warnNoUsageFile`, import de `time/tzdata`
- `server.go` -- `App.history`
- `proxy.go` -- `proxyV1` registra a request quando um tier atendeu
- `main.go` -- aviso e gravação final depois do `Shutdown`
- `history_test.go` -- testes da matriz
- `docs/configuration.md`, `docs/docker.md` -- `usage_file`, `timezone`, volume

**Revisão:** 8 achados (todos low), 2 patches aplicados (teste sem `usage_file`, rejeição de `Local`), 0 adiados, 6 rejeitados: mutex durante a escrita, falha de escrita sem retry ocioso, request depois do `close()`, `snapshot()` sem poda em memória, timers de teste, `usage_file` igual a `state_file`. Motivos no log de triagem.
**Follow-up recomendado:** false.
**Verificação:** `go vet ./...` e `go test -race ./...` verdes. O SIGTERM real não foi enviado; só o `close()` e a leitura de `main.go` cobrem a saída.
**Risco residual:** stream que termina depois dos 10 s do `Shutdown` não entra no arquivo; o modelo vem do cliente e as linhas não têm teto (comentado como `shortcut:`).
