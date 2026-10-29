---
title: "Failover do tier 0 para um tier extra com mapa de modelos"
ticket: 2
status: blocked
blocked_at: "2026-10-29"
blocked_reason: "prerequisite story 11 (config de tiers) is not built: no plan, and config.go has no tiers: support"
---

## Auto Run Result

- Status: blocked
- Blocking condition: a story 2.2 depende da 11 (`after = [11]`), que não foi construída: não há plano da 11 e `config.go` não lê `tiers:`.
- Next: rodar a story 11 (config de tiers) antes de reexecutar a 2.2.
