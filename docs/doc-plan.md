# Doc Plan: bud2 — 2026-04-05

Scoring: centrality (0.30) + coverage gap (0.30) + complexity (0.20) + churn (0.10) + bug density (0.10)
Qualitative override: +0.10 for cross-cutting concerns or (centrality + complexity) both in top quartile.

| Rank | Area | Score | Signals | Status |
|------|------|-------|---------|--------|
| 1 | `internal/executive` | 0.84 | 6 centrality, 9 files / 5193 LoC, 112 commits + 53 fix-commits/90d, no doc | missing (foundational) |
| 2 | `internal/mcp` | 0.84 | 14 centrality, 9 files / 5485 LoC (register.go alone is 87k chars), 68 commits/90d, no doc | missing (foundational) |
| 3 | `internal/types` | 0.72 | 28 centrality (most imported), no doc, 1 file / 214 LoC | missing (foundational) |
| 4 | `internal/integrations` | 0.65 | 16 centrality, no doc, 2 files / 1482 LoC | missing (foundational) |
| 5 | `cmd/bud` | 0.56 | no doc, 2 files / 1807 LoC, 121 commits + 55 fix-commits/90d (highest churn) | missing |
| 6 | `internal/reflex` | 0.53 | 8 centrality, no doc, 4 files / 3049 LoC | missing |
| 7 | `internal/focus` | 0.43 | 6 centrality, no doc, 3 files / 1142 LoC | missing |
| 8 | `internal/senses` | 0.41 | no doc, 2 files / 1382 LoC, 10 fix-commits/90d (highest bug rate per LoC) | missing |
| 9 | `internal/activity` | 0.40 | 6 centrality, no doc, 1 file / 935 LoC | missing |
| 10 | `internal/effectors` | 0.39 | no doc, 2 files / 1426 LoC | missing |
| 11 | `internal/budget` | 0.39 | 4 centrality, no doc, 3 files / 601 LoC | missing |
| 12 | `internal/state` | 0.38 | 4 centrality, no doc, 1 file / 525 LoC, 17 commits/90d | missing |
| 13 | `internal/memory` | 0.31 | 6 centrality, 5 files / 1178 LoC, partial coverage in v2-memory-architecture.md | partial |
| 14 | `internal/gtd` | 0.31 | 10 centrality, 3 files / 1028 LoC, partial coverage in things-integration.md | partial |
| 15 | `internal/engram` | 0.30 | 8 centrality, partial coverage in architecture/memory-research.md | partial |

## Recommended next

Run `dev:arch-doc internal/executive` on `internal/executive` — highest churn and bug density in the repo (112 commits, 53 fixes in 90 days) with no architectural doc; critical to understand before touching the session lifecycle.

---
_Generated: 2026-04-05T21:55:00Z | Commit: f18e5087_
