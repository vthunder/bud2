# Doc Plan: bud2 — 2026-04-09

Scoring: centrality (0.30) + coverage gap (0.30) + complexity (0.20) + churn (0.10) + bug density (0.10)
Topics span modules — signals are the max across constituent modules.

| Rank | Topic | Score | Key Modules | Signals | Status |
|------|-------|-------|-------------|---------|--------|
| 1 | Memory Quality Feedback Loop | 0.87 | `internal/executive`, `internal/engram` | centrality 8 (engram), no doc, 123 commits/90d, 57 fix-commits; RateEngrams() after signal_done is new. (foundational) | generated |
| 2 | Startup Lifecycle & Context Injection | 0.85 | `cmd/bud`, `internal/executive`, `seed/startup-instructions.md` | no doc, 108 commits/90d, 49 fix-commits; startup now injects structured instructions, memory retrieval disabled. (foundational) | missing |
| 3 | Session Lifecycle & Context Assembly | 0.80 | `internal/executive`, `internal/types` | centrality 33 (types), 123 commits/90d, 57 fix-commits; memory limit changed 10→6, startup path added. (foundational). Source: `session-lifecycle-context-assembly.md` | generated |
| 4 | Subagent Orchestration | 0.80 | `internal/executive`, `internal/types`, `internal/effectors` | centrality 33, 123 commits/90d, 57 fix-commits; startup subagent restart-notes pattern added. (foundational). Source: `subagent-orchestration.md` | generated |
| 5 | MCP Tool Dispatch & Registration | 0.58 | `internal/mcp`, `internal/types` | centrality 33 (via types), complexity max (5520 LoC, 9 files), 61 commits/90d. Source: `mcp-tool-dispatch-registration.md` | generated |
| 6 | Plugin Manifest Runtime & Tool Grants | 0.55 | `cmd/bud`, `internal/executive`, `state/system/plugins.yaml` | 108 commits/90d, 49 fix-commits; exclude list fix changes MCP server suppression. (foundational). Source: `plugin-manifest-runtime-tool-grants.md` | generated |
| 7 | Wake Scheduling & Autonomous Sessions | 0.55 | `internal/executive`, `internal/focus`, `internal/budget` | 123 commits/90d, 57 fix-commits; idle-fallback and startup handling updated. (foundational). Source: `wake-scheduling-autonomous-sessions.md` | generated |
| 8 | Zettel Library Discovery & Generation | 0.45 | `internal/executive/simple_session.go`, `cmd/bud` | 108 commits/90d; merge semantics changed (manual entries now preserved). Source: `zettel-library-discovery-generation.md` | generated |
| 9 | Token Budget & Session Caps | 0.45 | `internal/budget`, `internal/executive` | centrality 6, complexity max (5884 LoC), 123 commits/90d. Source: `token-budget-session-caps.md` | generated |
| 10 | Skill Grants & Agent Composition | 0.45 | `internal/executive/agent_defs.go`, `internal/executive/profiles.go`, `state/system/skill-grants.yaml` | centrality 6 (via executive), 123 commits/90d; exclude-list applied during manifest load. Source: `skill-grants-agent-composition.md` | generated |
| 11 | Reflex Evaluation Pipeline | 0.44 | `internal/reflex`, `internal/senses`, `internal/types` | centrality 33, 3049 LoC, 21 commits/90d. Source: `reflex-evaluation-pipeline.md` | generated |
| 12 | Attention & Salience Computation | 0.37 | `internal/focus`, `internal/types` | centrality 33 (via types), 17 commits/90d. Source: `attention-salience-computation.md` | generated |

## Recommended next

Run `dev:arch-doc "Startup Lifecycle & Context Injection"` on `bud2` — second-ranked missing topic; startup now injects structured instructions and the memory retrieval path differs from regular session startup.

---
_Generated: 2026-04-09T01:45:00Z | Commit: e790de89_
