# Doc Plan: bud2 — 2026-04-06

Scoring: centrality (0.30) + coverage gap (0.30) + complexity (0.20) + churn (0.10) + bug density (0.10)
Topics span modules — signals are the max across constituent modules.

| Rank | Topic | Score | Key Modules | Signals | Status |
|------|-------|-------|-------------|---------|--------|
| 1 | Session Lifecycle & Context Assembly | 1.00 | `internal/executive`, `internal/types`, `internal/memory` | centrality 28, no doc, 112 commits/90d, 53 fix-commits (foundational). Source: `executive-text-capture.md` | generated |
| 2 | MCP Tool Dispatch & Registration | 0.99 | `internal/mcp`, `internal/types` | centrality 28, complexity max (63.85), 68 commits/90d (foundational) | generated |
| 3 | Subagent Orchestration | 0.98 | `internal/executive`, `internal/types`, `internal/effectors` | centrality 28, no doc, 112 commits/90d, 53 fix-commits | generated |
| 4 | Reflex Evaluation Pipeline | 0.84 | `internal/reflex`, `internal/senses`, `internal/types` | centrality 28, no doc, 22 commits/90d, 8 fix-commits (foundational) | generated |
| 5 | Attention & Salience Computation | 0.77 | `internal/focus`, `internal/types` | centrality 28, no doc, 16 commits/90d (foundational) | generated |
| 6 | Wake Scheduling & Autonomous Sessions | 0.74 | `internal/executive`, `internal/focus`, `internal/budget` | no doc, 112 commits/90d, 53 fix-commits, high complexity | missing |
| 7 | Token Budget & Session Caps | 0.74 | `internal/budget`, `internal/executive` | no doc, 112 commits/90d (inherited), 53 fix-commits, high complexity | missing |
| 8 | Percept Ingestion & Senses | 0.69 | `internal/senses`, `internal/memory`, `internal/types` | centrality 28, no doc, 26 commits/90d. Source: `architecture/message-flow.md` | missing |
| 9 | Seed Configuration & Plugin System | 0.66 | `cmd/bud`, `seed/` | no doc, 121 commits/90d (highest churn), 55 fix-commits (foundational) | missing |
| 10 | External Integration Clients | 0.56 | `internal/integrations`, `internal/senses` | centrality 16, no doc, cross-cutting | missing |
| 11 | Memory Consolidation Pipeline | 0.33 | `internal/engram`, `internal/memory`, `internal/embedding`, `internal/eval` | centrality 8, doc exists ~84d old, 26 commits/90d. Source: `architecture/v2-memory-architecture.md` | stale |
| 12 | GTD & Task Integration | 0.31 | `internal/gtd`, `things-mcp/` | centrality 10, low churn. Source: `things-integration.md` | stale |

## Recommended next

Run `dev:arch-doc "Wake Scheduling & Autonomous Sessions"` on `bud2` — ranks 1–5 are now generated; Wake Scheduling & Autonomous Sessions is the next highest-priority undocumented topic (rank 6, score 0.74).

---
_Generated: 2026-04-06T00:08:00Z | Commit: 34809241_
