---
id: 2
type: epic
title: "O proxy troca de provedor quando um falha"
parent: initiative-multi-provider
covers: [R1, R2, R3, R4, R6, R7, R8, R10, R11, R12]
sources: []
companions: []
after: []
refined: true
risk: high
---

# O proxy troca de provedor quando um falha

## Description

O proxy atende a request pelo primeiro tier elegível, em ordem de prioridade, e passa ao próximo quando um falha. O tier 0 é o OpenCode Go com o pool de chaves atual, que continua girando por dentro do tier. Cada tier tem mapa de modelos, estilos de API e a flag de gratuito; nesta fase só tiers gratuitos são aceitos. O cooldown dos tiers sobrevive a reinício e cada request gera um log estruturado. Entrega ao relatório e ao catálogo o log, o estado persistido, a lista de tiers e o formato da config; entrega à página a seção de tiers.

## Outcome

Para o Eduardo e os agentes Hermes: uma request não falha por cota esgotada enquanto algum tier elegível responder, sem mudar nenhum agente; o sinal é um teste com o OpenCode Go esgotado em que o próximo tier mapeado atende.

## Requirements

- R1 (parent R1): O proxy escolhe tiers por prioridade, OpenCode Go = 0, depois os demais na ordem da config; se um tier falha, o próximo atende a request.
- R2 (parent R2): Cada tier tem um mapa de modelos; um modelo sem mapeamento faz o tier ser pulado.
- R3 (parent R3): Cada tier declara os estilos de API que fala (OpenAI, Anthropic) e é pulado se não falar o do request; não há tradução entre estilos.
- R4 (parent R4): Toda a config fica no proxy; os agentes mantêm o mesmo URL e a mesma `PROXY_API_KEY`.
- R6 (parent R6): Tiers gratuitos (`free: true`) entram como os demais.
- R7 (parent R7): 429, 5xx, 401, 402 e 404 do upstream colocam o tier em cooldown e a request segue ao próximo; 400 não faz failover. Erro de conexão e timeout antes do primeiro byte da resposta também fazem failover.
- R8 (parent R8): Um modelo só é elegível num tier que tenha o contexto mínimo e o tool calling que o Hermes precisa.
- R10 (parent R10): O estado de cooldown sobrevive a um reinício do proxy. O gasto do dia é do épico de relatório.
- R11 (parent R11): Cada request gera um log estruturado com modelo, estilo, chave ou tier, status e latência.
- R12 (parent R12): A página de status mostra os tiers, com estado e cooldown; as chaves continuam como no épico da página.

## Constraints

- Sem tradução entre estilos de API (parent, Constraints).
- Privacidade aceita: tiers gratuitos recebem o contexto como os demais (parent, Constraints).
- Persistência em arquivo JSON local escrito de forma atômica, sem banco (parent, Notes, Decision de 2026-10-29).
- A config atual (`upstream:` e `OPENCODE_GO_API_KEYS`) continua valendo como tier 0; só os demais tiers entram numa lista `tiers:` nova (R4).

## Done when

- Com o OpenCode Go esgotado, uma request é atendida pelo próximo tier com modelo mapeado, verificado contra upstreams de teste.
- 401, 402, 404, 5xx, 429 e erro de conexão antes do primeiro byte põem o tier em cooldown e passam ao próximo; 400 volta ao cliente.
- Um tier sem mapeamento para o modelo, ou que não fala o estilo do request, é pulado; um modelo sem o contexto mínimo ou sem tool calling não é elegível.
- Uma config com tier pago (sem `free: true`) é rejeitada na partida, até o épico de relatório entregar o teto.
- O cooldown dos tiers permanece depois de reiniciar o proxy.
- Cada request grava uma linha de log com modelo, estilo, tier, status e latência, e a página de status mostra os tiers.

## Boundaries

- Fora: tradução entre estilos, proxy key por agente, tiers pagos e o teto diário (épico de relatório), relatório e catálogo.
- Touch point: `KeyManager` e `proxyV1` em `main.go` — owner: este épico
- Touch point: a página de status — owner: epic-pagina-de-status; este épico acrescenta a seção de tiers
- Must not change: o URL e a `PROXY_API_KEY` que os agentes Hermes usam, e a config atual de chaves.

## References

- parent — _bmad-output/initiative-multi-provider/initiative-multi-provider.md, section Requirements
- forge — _bmad-output/forge-multi-provider/forge-multi-provider.md, Decisões e A verificar na construção
- code — `main.go` (`KeyManager`, `proxyV1`, `isKeyDead`, `isQuota429`), `adminkeys.go`

## Notes

- Decision (2026-10-29): o estado persiste em arquivo JSON local escrito de forma atômica, sem banco.
- Decision (2026-10-29): a primeira story separa `main.go` em arquivos dentro do `package main`, sem pacotes nem interfaces novas.
- Decision (2026-10-29): o tracer é a story 2, o failover do tier 0 para o tier 1 com mapa de modelos.
- Decision (2026-10-29): o teto diário dos tiers pagos espera o dinheiro: R5 passa ao épico de relatório, que tem a tabela de preços e os tokens. Até lá, config com tier não gratuito é rejeitada na partida. Declinada a alternativa de teto em requests por dia (John: não mede gasto).
- Decision (2026-10-29): o gasto do dia (parte de R10) passa ao épico de relatório junto com o teto; aqui persiste só o cooldown.
- Decision (2026-10-29): erro de conexão e timeout fazem failover só antes do primeiro byte da resposta; depois o stream já começou.
- Decision (2026-10-29): as chaves do OpenCode Go continuam girando dentro do tier 0; o tier só falha quando todas estão em cooldown.
- Decision (2026-10-29): cooldown por tier, campo `cooldown` com padrão `retry_exhausted_after` (5 min); ajustado depois pelo log.
- Decision (2026-10-29): o estilo real do Hermes se vê no log da story 3; sem spike.
- Decision (2026-10-29): fecha com a story "Refactor sweep".
- Source conflict: R7 — o código só troca de chave em 429 de cota e 403 de assinatura (`isKeyDead`); 401, 402, 404 e 5xx passam ao cliente e erro de conexão vira 502 sem failover. Este épico muda isso.
- Decision (2026-10-29): R7 muda o comportamento atual; 401, 402, 404, 5xx e erro de conexão passam a fazer failover.
