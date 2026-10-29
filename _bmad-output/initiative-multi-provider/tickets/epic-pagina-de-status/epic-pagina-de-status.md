---
id: 1
type: epic
title: "O Eduardo vê o estado das chaves numa página"
parent: initiative-multi-provider
covers: [R, 1, 2]
sources: []
companions: []
after: []
refined: false
risk: low
---

# O Eduardo vê o estado das chaves numa página

## Description

Uma página HTML servida pelo próprio proxy mostra o estado, o cooldown e a porcentagem de uso de cada chave, lendo os dados que o `/admin/keys` já produz. É a primeira entrega da iniciativa e não depende do roteamento por tiers.

## Outcome

Para o Eduardo: saber como está o roteador abrindo um endereço no navegador, sem curl nem Claude Code; o sinal é a página mostrar cada chave com estado, cooldown e uso.

## Done when

- A página mostra, para cada chave, nome, estado, cooldown e porcentagem de uso do `/admin/keys`.
- A página e os dados que ela lê funcionam sem autenticação e nunca expõem o valor de uma chave.
- As ações do admin que alteram estado (reset de chave) continuam protegidas pela `PROXY_API_KEY`.
- A página vai no binário e na imagem Docker, e foi aberta contra o proxy rodando.
- `docs/operations.md` diz que a página é aberta e que, com URL público, a porta deve ser restrita por rede.

## Boundaries

- Fora: seção de tiers (épico roteamento-por-tiers) e relatório (épico relatorio-de-uso-e-custo).
- Touch point: `handleKeysReport` em `adminkeys.go` e as rotas `/admin` em `main.go` — owner: este épico
- Must not change: as ações do admin que alteram estado seguem exigindo a `PROXY_API_KEY`.

## References

- parent — _bmad-output/initiative-multi-provider/initiative-multi-provider.md, section Requirements
- decision — initiative-multi-provider.md, Notes, página sem autenticação (2026-10-29)
- code — `adminkeys.go`, `main.go` (`handleAdmin`)

## Notes

- Open question: os dados abertos saem do `/admin/keys` sem autenticação ou de uma rota nova só de leitura? As rotas que alteram estado precisam continuar protegidas.
