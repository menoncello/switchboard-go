---
id: 4
type: epic
title: "O Eduardo recebe propostas de modelos e limites novos"
parent: initiative-multi-provider
covers: [R9]
sources: []
companions: []
after: []
refined: true
risk: medium
---

# O Eduardo recebe propostas de modelos e limites novos

## Description

Um agente recorrente, configurado pelo Eduardo no Orca, pesquisa modelos novos, limites novos, mudanças de preço e de gratuidade, e provedores novos com tier gratuito, e grava propostas de mudança em arquivo próprio. Só propõe: nunca altera o roteamento. Auto-adição, começando pelo OpenRouter (preço via API), fica para depois de ver a qualidade das propostas.

## Outcome

Para o Eduardo: ser avisado de mudanças no catálogo antes de gastar sem querer; o sinal é um aviso no Telegram quando uma proposta aponta um modelo que passou de gratuito a pago.

## Requirements

- R9 (parent R9): A automação de catálogo (modelos novos, limites novos, quem deixou de ser gratuito) só propõe e nunca altera o roteamento.

## Constraints

- A proposta é lida por uma pessoa; o proxy não lê o arquivo de propostas (parent, R9).
- Preço legível por máquina só no OpenRouter; nos demais a fonte é a página de documentação, citada em cada item (parent, Constraints).
- O agente roda fora do proxy, com a config só para leitura (Notes, Decision de 2026-10-30).

## Done when

- Uma rodada grava uma proposta com modelos novos, limites novos, mudanças de preço ou disponibilidade e mudanças de gratuidade, cada item com a URL da fonte.
- A proposta traz o hash da config, do `state_file` e do `usage_file` (os que o agente alcança) do início e do fim da rodada e a linha "roteamento inalterado: sim/não", que depende só do hash da config.
- Um teste confirma que o proxy não lê o arquivo de propostas.
- Um item já proposto numa rodada anterior e sem mudança de valor não se repete.
- Um modelo que passou de gratuito a pago gera aviso no Telegram.
- A proposta inclui provedores fora da config que ofereçam tier gratuito.
- O agente roda sozinho em intervalo recorrente, configurado no Orca.

## Boundaries

- Fora: auto-adição de modelos e qualquer escrita na config de tiers.
- Touch point: o formato da config de tiers — owner: epic-roteamento-por-tiers; a tabela de preços (`prices`) — owner: epic-relatorio-de-uso-e-custo
- Must not change: a config de tiers em uso.

## References

- parent — _bmad-output/initiative-multi-provider/initiative-multi-provider.md, section Requirements
- forge — _bmad-output/forge-multi-provider/forge-multi-provider.md, Decisões (automação de catálogo)
- code — `config.go` (`yamlTier`, `Prices`, `ModelPrice`), `telegram.go`

## Notes

- Decision (2026-10-29): este épico ficou só registrado, sem stories, até o roteamento e o relatório existirem. Os dois estão entregues; refinado e fatiado em 2026-10-30.
- Decision (2026-10-30): a automação é um agente, configurado e agendado pelo Eduardo no Orca; o repo guarda só as instruções do agente e a documentação, sem código novo no proxy.
- Decision (2026-10-30): uma proposta em Markdown por rodada, ao lado do `state_file`, cada item com a URL da fonte.
- Decision (2026-10-30): preço e gratuidade: OpenRouter pela API; os demais provedores pela página de documentação, lida pelo agente, com a fonte citada; se a página mudar de formato, o item sai marcado "não verificado". Resolve a open question do parent sobre preço fora do OpenRouter.
- Decision (2026-10-30): "novo" é o que difere da config atual (`tiers`, `prices`) e da proposta da rodada anterior, incluindo mudança de valor ou de disponibilidade.
- Decision (2026-10-30): o agente também descobre provedores fora da config com tier gratuito, em story própria depois da dos provedores já configurados.
- Decision (2026-10-30): aviso no Telegram só quando a proposta tem item de gratuidade perdida; o resto fica no arquivo.
- Decision (2026-10-30): fatiado em 8 stories numa trilha só (4.1 a 4.8), porque todas mexem em `docs/catalog-agent.md`. O tracer é a story 1 (rodada com OpenRouter até a proposta); a mais incerta, a 3 (páginas de documentação), vem logo depois da guarda de roteamento (2). Instruções do agente em `docs/catalog-agent.md` (confirmado pelo Eduardo). Fecha com "Refactor sweep", mantido por decisão do Eduardo; sem suíte e2e, a fonte não tem plano de testes. Adiados: auto-adição de modelos e o aviso SMTP na troca de tier.
- Decision (2026-10-30): "roteamento idêntico" se prova em três camadas: hash da config, do `state_file` e do `usage_file` no início e no fim de cada rodada, gravados na proposta, com a linha "roteamento inalterado" dependendo só do hash da config (o proxy reescreve os outros dois sozinho); um teste de que o proxy não lê o arquivo de propostas; e a config só para leitura para o agente (passo do Eduardo no Orca). Declinado testar a saída do agente byte a byte, porque varia a cada rodada.
- Decision (2026-10-30): o agente grava as propostas num diretório que o Eduardo define, padrão ao lado do `state_file` quando alcançável, porque o proxy pode rodar em outro host; o hash cobre só os arquivos que o agente alcança.
- Decision (2026-10-30): verifies das stories 4.3 (páginas reais) e 4.6 (Telegram real) são `hitl`; 4.4 e 4.5 rodam pelo construtor como ensaio com fontes de exemplo.
