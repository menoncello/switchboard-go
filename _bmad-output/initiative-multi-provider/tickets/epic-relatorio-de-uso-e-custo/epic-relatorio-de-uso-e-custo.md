---
id: 3
type: epic
title: "O Eduardo sabe quanto cada projeto e modelo usou e custou"
parent: initiative-multi-provider
covers: [R5, R10, R13, R14]
sources: []
companions: []
after: []
refined: true
risk: medium
---

# O Eduardo sabe quanto cada projeto e modelo usou e custou

## Description

Um relatório agrega uso e custo por dia, projeto, modelo, chave e tier, e a página de status o mostra. O projeto vem do cabeçalho opcional `X-Project`, que o proxy consome e não repassa ao upstream. O custo vem de uma tabela de preços na config, preenchida à mão; sem preço, o relatório mostra só tokens. Estende o log do roteamento com projeto e tokens e é dono do histórico de uso, da tabela de preços e do teto diário em dinheiro dos tiers pagos: config de tier pago sem teto é rejeitada na partida, o tier é pulado ao atingir o teto e o primeiro uso dispara o alerta SMTP. O gasto do dia sobrevive a reinício.

## Outcome

Para o Eduardo: saber o uso e o gasto por projeto e modelo; o sinal é o relatório separar duas requests com `X-Project` diferentes.

## Requirements

- R5 (parent R5): Todo tier pago tem teto diário global, compartilhado entre agentes, em dinheiro; uma config sem ele é rejeitada na partida; o primeiro uso de um tier pago dispara o alerta SMTP.
- R10 (parent R10): O gasto do dia sobrevive a um reinício do proxy. O cooldown é do épico de roteamento.
- R13 (parent R13): Um relatório de uso e custo agrega por dia, projeto, modelo, chave e tier; o custo vem de uma tabela de preços na config, e sem preço o relatório mostra só tokens.
- R14 (parent R14): O projeto é identificado pelo cabeçalho opcional `X-Project`; sem ele a request fica "sem projeto"; o proxy não repassa o cabeçalho ao upstream e limita tamanho e caracteres do valor.

## Constraints

- Persistência em arquivo JSON local escrito de forma atômica, sem banco (parent, Notes, Decision de 2026-10-29).
- A página de status é renderizada no servidor, sem API nova e sem mostrar valor de chave (parent, Notes, Decision de 2026-10-29).
- Preço legível por máquina só no OpenRouter; a tabela é preenchida à mão (parent, Constraints; R13).
- Sem proxy key por projeto (parent, Constraints).

## Done when

- O relatório agrega por dia, projeto, modelo, chave e tier.
- O custo em dinheiro aparece onde a tabela tem preço; onde não tem, só tokens.
- Uma request sem `X-Project` entra como "sem projeto"; o cabeçalho não chega ao upstream e o valor respeita o limite de tamanho e caracteres.
- O histórico de uso permanece depois de reiniciar o proxy.
- Um tier pago só é aceito com `daily_cap`, `usage_file` e preço para todos os seus modelos; o tier é pulado ao atingir o teto, o gasto do dia sobrevive a reinício e o primeiro uso do dia dispara o alerta SMTP.
- `usage_file`, `prices`, `daily_cap` e `timezone` estão documentados em `docs/configuration.md`.
- A página de status mostra o relatório.

## Boundaries

- Fora: proxy key por projeto (adiada) e qualquer ação sobre o roteamento.
- Touch point: o log e o estado persistido do épico roteamento-por-tiers — owner: epic-roteamento-por-tiers
- Must not change: o log por request já entregue continua com os mesmos campos.

## References

- parent — _bmad-output/initiative-multi-provider/initiative-multi-provider.md, section Requirements
- decision — initiative-multi-provider.md, Notes, X-Project e custo (2026-10-29)

## Notes

- Open question: o Hermes consegue mandar o cabeçalho `X-Project`? Sem isso o relatório por projeto fica vazio.
- Decision (2026-10-29): R5 (teto diário) e a parte de R10 sobre o gasto do dia vieram do épico de roteamento; o teto é em dinheiro, não em requests, e tiers pagos só entram depois dele.
- Decision (2026-10-29): relatório numa seção de `/status`, sem API nova: total do dia no topo e uma tabela por dimensão (dia, projeto, modelo, chave, tier) dos últimos 7 dias em `<details>`, com o fuso ao lado do total.
- Decision (2026-10-29): histórico em `usage_file` próprio (JSON atômico), agregado por dia, projeto, modelo, chave e tier, com gravação atrasada de poucos segundos e na saída, e retenção de 90 dias; o `state_file` do roteamento fica só com cooldown.
- Decision (2026-10-29): tokens vêm do `usage` da resposta (JSON e SSE, nos dois estilos) sem alterar o request; só nos tiers pagos em stream o proxy injeta `include_usage`. Resposta de tier pago sem `usage` conta o preço máximo do mapa como pior caso e entra na coluna "sem usage" do relatório; declinada a estimativa pelo tamanho do corpo (Winston, Amelia).
- Decision (2026-10-29): preço em USD por 1M tokens, por tier e id do modelo mapeado; o tier 0 é assinatura e fica só em tokens.
- Decision (2026-10-29): tier pago exige `daily_cap` em USD, `usage_file` e preço para todos os seus modelos; sem `usage_file` o gasto zeraria no reinício.
- Decision (2026-10-29): o dia é calculado no fuso `timezone` (nome IANA, padrão UTC), igual para relatório e teto; `tzdata` embutido no binário; a request em andamento pode passar do teto, porque o custo só se conhece no fim. Declinado o padrão `America/Sao_Paulo` (John).
- Decision (2026-10-29): `X-Project` aceita até 64 caracteres `[A-Za-z0-9._-]`; valor inválido vira "sem projeto" e a request segue.
- Decision (2026-10-29): o alerta SMTP de primeiro uso dispara uma vez por tier por dia e não se repete após reinício.
- Decision (2026-10-29): a story do alerta SMTP (7) fica separada da do teto (6), `risk = low`; John queria dobrá-la na 6 (declinado).
- Decision (2026-10-29): o tracer é a story 4, a seção de relatório em `/status`; trilha única 3.1 a 3.8, porque todas mexem em `proxy.go`, `server.go` e `/status`. Fecha com "Refactor sweep"; sem suíte e2e, a fonte não tem plano de testes.
- Parked: o aviso SMTP na troca de tier (`deferred-work.md`) fica fora do épico.
