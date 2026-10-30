---
title: 'Tokens por request no log, de JSON e de stream'
type: 'feature'
ticket: 2
created: '2026-10-29'
status: 'built'
baseline_revision: 'd4dfcc4a702122d814295f49e59ae39fff9b822b'
route: 'full'
route_source: 'auto'
risk: 'high'
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

**Problem:** O log por request não diz quantos tokens a resposta custou, e sem isso não há relatório de uso (R13).

**Approach:** Ler o `usage` da resposta do upstream enquanto `copyResponse` a repassa (JSON inteiro e SSE, estilos OpenAI e Anthropic), sem esperar o fim do stream e sem tocar no request, e gravar `tokens_in` e `tokens_out` na linha de log. Resposta 2xx sem `usage` grava o log sem tokens e com `usage_missing: true`.

## Boundaries & Constraints

**Always:** os bytes e o momento de cada flush do stream chegam ao cliente como hoje; os campos já existentes do log não mudam; leitura do `usage` nunca falha a request (corpo malformado só vira `usage_missing`); o buffer do corpo JSON tem teto.

**Never:** alterar o request (nada de `include_usage`, isso é de story posterior); estimar tokens pelo tamanho do corpo; gravar `usage_missing` ou tokens em resposta não 2xx e em resposta sem tier (429 local, 502, 499, 413, 400).

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| JSON OpenAI | `usage:{prompt_tokens:11,completion_tokens:7}` | `tokens_in=11`, `tokens_out=7` | — |
| JSON Anthropic | `usage:{input_tokens:12,output_tokens:5}` | `tokens_in=12`, `tokens_out=5` | — |
| Stream Anthropic | `message_start` (`message.usage.input_tokens`) e `message_delta` (`usage.output_tokens`, cumulativo) | in do start, out do último delta | — |
| Stream OpenAI | último chunk com `usage` antes de `[DONE]` | tokens desse chunk | — |
| Sem usage | 200 JSON ou stream sem `usage` | log sem `tokens_*`, com `usage_missing=true` | request segue |
| Linha SSE cortada entre leituras | evento dividido em dois reads | ainda lido | — |
| Corpo não 2xx | 401, 429, 5xx | sem `tokens_*` e sem `usage_missing` | — |

</intent-contract>

## Code Map

- `proxy.go` -- `copyResponse` (l.431) repassa JSON por `io.Copy` e SSE em laço de 4096 bytes com `Flush`; é chamada em 3 pontos: tier 0 OK (l.178), tier extra OK (`proxyTiers`, l.321) e resposta guardada (`last.resp`, l.196). `serveProxy` devolve `(model, tier, keyIdx)` e `proxyV1` monta `attrs` do log (l.112-117).
- `usage.go` (novo) -- tipo `usageTokens{in, out int; found bool}` e o leitor; sem dependência nova, `encoding/json` do stdlib.
- `main_test.go` -- `TestRequestLogProjectHeader` (l.2003) mostra o padrão: `newApp(Config{...})`, `captureLog(app)`, `tierReq(...)`, `newStub`.
- `docs/configuration.md` -- seção "Request log" (l.225): exemplo de linha e lista de campos.

## Tasks & Acceptance

**Execution:**
- [ ] `usage.go` -- `parseUsage(raw []byte) usageTokens` (aceita `usage` no topo ou em `message.usage`; `prompt_tokens`/`input_tokens` e `completion_tokens`/`output_tokens`) e `sseUsageScanner` (`io.Writer` que separa linhas, ignora linha sem `"usage"`, lê `data: {json}` e guarda o último `in`/`out` não zero, mantendo `in` do `message_start`) -- extração isolada e testável
- [ ] `proxy.go` -- `copyResponse` devolve `usageTokens`: no SSE alimenta o scanner com cada chunk já escrito e flushado; no JSON copia com `io.TeeReader` para buffer limitado (1 MiB; acima disso, ignora) e faz `parseUsage`; com `Content-Encoding` presente não lê (vira `usage_missing`). `serveProxy` e `proxyV1` propagam o valor ao log: `tokens_in`, `tokens_out` quando `found`, `usage_missing=true` quando 2xx do tier e não `found`
- [ ] `main_test.go` -- testes da matriz: JSON OpenAI, JSON Anthropic, stream Anthropic, stream OpenAI com `usage` no último chunk, sem usage (JSON e stream), não 2xx, evento SSE dividido em dois writes, e um stream cujo primeiro evento chega ao cliente antes de o upstream enviar o resto (sem espera até o fim) com bytes idênticos aos do upstream
- [ ] `docs/configuration.md` -- documentar `tokens_in`, `tokens_out` e `usage_missing` na seção Request log, com a fonte (campo `usage` da resposta) e os casos em que faltam

**Acceptance Criteria:**
- Given um upstream de teste respondendo JSON OpenAI, JSON Anthropic, stream Anthropic ou stream OpenAI com `usage`, when o proxy atende a request, then a linha de log traz `tokens_in` e `tokens_out` certos.
- Given um stream do upstream que envia um evento e espera, when o cliente lê, then recebe o evento antes de o upstream terminar e o corpo final é byte a byte igual ao do upstream.
- Given uma resposta 2xx sem `usage`, when logada, then não há `tokens_*` e há `usage_missing: true`.
- Given o request enviado ao upstream, when comparado antes e depois da mudança, then é idêntico.

## Implementation Notes

## Plan Change Log

## Review Triage Log

### 2026-10-29 — Review pass
- verdicts: 6 findings — high 0, medium 0, low 6, false 0, maybe-false 0
- findings:
  - `[low]` `[reject]` `parseUsage` descarta o resultado quando `message` de topo é string — só ocorre com corpo 2xx fora dos formatos OpenAI e Anthropic; o conserto acrescenta decodificação extra.
  - `[low]` `[reject]` `usage` com 0 e 0 vira `usage_missing` — leitura do "não zero" do ticket; sem dano.
  - `[low]` `[reject]` Stream Anthropic cortado após `message_start` loga `tokens_out` placeholder — raro, e o conserto exige marcar parcial no log.
  - `[low]` `[patch]` Exemplo de linha em `docs/configuration.md` sem os campos novos — exemplo agora traz `tokens_in` e `tokens_out`.
  - `[low]` `[reject]` Lacunas de teste (Content-Encoding, JSON acima de 1 MiB, tier extra, 429/5xx) — o retorno do tier extra é plumbing checado pelo compilador; as regras sem teste são desvios simples.
  - `[low]` `[reject]` Ramo `track && isStream` sem `Flusher` é inalcançável via `statusRecorder` — é o mesmo fallback que já existia (o `else`), inofensivo.

## Auto Run Result

**Resumo:** o proxy lê o `usage` da resposta (JSON inteiro e SSE, estilos OpenAI e Anthropic) enquanto a repassa e grava `tokens_in` e `tokens_out` no log; 2xx de um tier sem `usage` grava `usage_missing: true`. O request ao upstream e o fluxo do stream não mudam.

**Arquivos:**
- `usage.go` -- `parseUsage`, `sseUsageScanner`, `cappedBuffer`
- `proxy.go` -- `copyResponse` devolve `usageTokens`; `serveProxy`, `proxyTiers` e `proxyV1` propagam e logam
- `main_test.go` -- `TestRequestLogTokens`, `TestSSEUsageScannerSplitEvent`, `TestStreamReachesClientBeforeUpstreamEnds`
- `docs/configuration.md` -- campos novos e exemplo de linha na seção Request log

**Revisão:** 6 achados (todos low), 1 patch aplicado (exemplo da doc), 0 adiados, 5 rejeitados: `message` string em `parseUsage`, usage 0/0 como ausente, `tokens_out` placeholder em stream cortado, lacunas de teste (Content-Encoding, >1 MiB, tier extra), ramo sem `Flusher` inalcançável. Motivos no log de triagem.
**Follow-up recomendado:** false.
**Verificação:** `go vet ./...` e `go test ./...` verdes.
**Risco residual:** resposta comprimida e JSON acima de 1 MiB viram `usage_missing`, sem teste; o critério "request idêntico ao upstream" vale só porque `doUpstream` não mudou.

## Design Notes

Anthropic informa `input_tokens` no `message_start` e o `output_tokens` cumulativo no `message_delta`; por isso o scanner guarda o último valor não zero de cada lado em vez de somar. `usage:null` do OpenAI em chunks intermediários é ignorado.

`shortcut:` resposta comprimida (`Content-Encoding`) e JSON acima de 1 MiB viram `usage_missing`; tratar quando algum upstream real comprimir.

## Verification

**Commands:**
- `go vet ./...` -- expected: sem aviso
- `go test ./...` -- expected: verde, com os testes novos de tokens
