# Doc Plan: bud2 — 2026-04-08

Scoring: centrality (0.30) + coverage gap (0.30) + complexity (0.20) + churn (0.10) + bug density (0.10)
Topics span modules — signals are the max across constituent modules.

| Rank | Topic | Score | Key Modules | Signals | Status |
|------|-------|-------|-------------|---------|--------|
| 1 | Plugin Manifest Runtime & Tool Grants | 0.54 | `cmd/bud`, `internal/executive/simple_session.go`, `state/system/plugins.yaml` | no doc covering exclude + zettel-libraries additions (7 commits behind), 117 commits/90d, 56 fix-commits, foundational plugin security boundary. (foundational) | stale |
| 2 | Zettel Library Discovery & Generation | 0.48 | `internal/executive/simple_session.go`, `cmd/bud/main.go` | no doc, generated at startup from plugin manifests; cache-readonly semantics; 117 commits/90d. (foundational) | generated |
| 3 | Session Lifecycle & Context Assembly | 0.36 | `internal/executive`, `internal/types`, `internal/memory` | centrality 6+33, 119 commits/90d, 54 fix-commits, doc fresh ~2d. (foundational). Source: `session-lifecycle-context-assembly.md` | generated |
| 4 | Wake Scheduling & Autonomous Sessions | 0.35 | `internal/executive`, `internal/focus`, `internal/budget` | 119 commits/90d, doc fresh ~2d; idle-fallback + Things-task-check covered. (foundational). Source: `wake-scheduling-autonomous-sessions.md` | generated |
| 5 | Skill Grants & Agent Composition | 0.34 | `internal/executive/agent_defs.go`, `internal/executive/profiles.go`, `state/system/skill-grants.yaml` | centrality 6 (via executive), doc fresh ~1d; centralized grant system. (foundational). Source: `skill-grants-agent-composition.md` | generated |
| 6 | MCP Tool Dispatch & Registration | 0.34 | `internal/mcp`, `internal/types` | centrality 14+33, complexity max (5513 LoC, 9 files), doc fresh ~2d. Source: `mcp-tool-dispatch-registration.md` | generated |
| 7 | Subagent Orchestration | 0.33 | `internal/executive`, `internal/types`, `internal/effectors` | centrality 6+33, 119 commits/90d, doc fresh ~2d. Source: `subagent-orchestration.md` | generated |
| 8 | Seed Configuration & Plugin System | 0.32 | `cmd/bud`, `seed/` | 117 commits/90d, doc fresh ~1d; plugins.yaml runtime + exclude lists added since generation. Source: `seed-configuration-plugin-system.md` | stale |
| 9 | Reflex Evaluation Pipeline | 0.31 | `internal/reflex`, `internal/senses`, `internal/types` | centrality 9+33, 21 commits/90d, doc fresh ~2d. Source: `reflex-evaluation-pipeline.md` | generated |
| 10 | Attention & Salience Computation | 0.30 | `internal/focus`, `internal/types` | centrality 6+33, 17 commits/90d, doc fresh ~2d. Source: `attention-salience-computation.md` | generated |
| 11 | Token Budget & Session Caps | 0.29 | `internal/budget`, `internal/executive` | 12 commits/90d, doc fresh ~1d. Source: `token-budget-session-caps.md` | generated |
| 12 | Percept Ingestion & Senses | 0.28 | `internal/senses`, `internal/memory`, `internal/types` | centrality 2+33, 19 commits/90d, doc fresh ~2d. Source: `percept-ingestion-senses.md` | generated |
| 13 | Memory Consolidation Pipeline | 0.27 | `internal/engram`, `internal/memory`, `internal/embedding`, `internal/eval` | centrality 8, 22 commits/90d, doc fresh ~1d. Source: `memory-consolidation-pipeline.md` | generated |
| 14 | GTD & Task Integration | 0.26 | `internal/gtd`, `things-mcp/` | centrality 14, low churn, doc fresh ~1d. Source: `gtd-task-integration.md` | generated |

## Recommended next

Run `dev:arch-doc "Plugin Manifest Runtime & Tool Grants"` on `bud2` — existing doc is stale (7 commits behind; exclude lists and zettel-libraries additions not covered).

---
_Generated: 2026-04-08T05:45:00Z | Commit: dcd6fdfd_
