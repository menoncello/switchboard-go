---
id: 3
type: epic
title: "O Eduardo sabe quanto cada projeto e modelo usou e custou"
parent: initiative-multi-provider
covers: [R5, R13, R14]
sources: []
companions: []
after: []
refined: false
risk: medium
---

# O Eduardo sabe quanto cada projeto e modelo usou e custou

## Description

Um relatório agrega uso e custo por dia, projeto, modelo, chave e tier, e a página de status o mostra. O projeto vem do cabeçalho opcional `X-Project`, que o proxy consome e não repassa ao upstream. O custo vem de uma tabela de preços na config, preenchida à mão; sem preço, o relatório mostra só tokens. Estende o log do roteamento com projeto e tokens e é dono do histórico de uso, da tabela de preços e do teto diário em dinheiro dos tiers pagos: config de tier pago sem teto é rejeitada na partida, o tier é pulado ao atingir o teto e o primeiro uso dispara o alerta SMTP. O gasto do dia sobrevive a reinício.

## Outcome

Para o Eduardo: saber o uso e o gasto por projeto e modelo; o sinal é o relatório separar duas requests com `X-Project` diferentes.

## Done when

- O relatório agrega por dia, projeto, modelo, chave e tier.
- O custo em dinheiro aparece onde a tabela tem preço; onde não tem, só tokens.
- Uma request sem `X-Project` entra como "sem projeto"; o cabeçalho não chega ao upstream e o valor respeita o limite de tamanho e caracteres.
- O histórico de uso permanece depois de reiniciar o proxy.
- Um tier pago só é aceito com teto diário em dinheiro; o tier é pulado ao atingir o teto, o gasto do dia sobrevive a reinício e o primeiro uso dispara o alerta SMTP.
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
