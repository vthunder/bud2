---
generated_at: 2026-04-05T21:55:00Z
commit: f18e5087
repomix: available
---

# bud2 — Overview

> Generated: 2026-04-05 | Commit: f18e5087

## Purpose

Bud is a personal AI agent that runs as a macOS launchd daemon, providing autonomous assistance, long-term memory, and task management through a Discord interface. Built in Go, it wraps the Claude Agent SDK to give Claude an event loop, attention system, and a rich set of MCP tools for interacting with external services and its own state.

## Data Flow

External signals arrive through senses (`internal/senses/discord.go` for Discord messages, `internal/senses/calendar.go` for calendar events). Each sense creates a **percept** in the `PerceptPool` (`internal/memory/percepts.go`) and enqueues a **focus item** into `focus.Queue` (`internal/focus/queue.go`). The `focus.Attention` system (`internal/focus/attention.go`) computes salience (priority × source boost × recency) and selects the highest-priority item. Before reaching the executive, the **reflex engine** (`internal/reflex/engine.go`) evaluates YAML rules from `seed/reflexes/` — simple queries are answered directly via Discord without waking Claude.

For items that pass through reflexes, `ExecutiveV2` (`internal/executive/executive_v2.go`) opens a Claude session via the Claude Agent SDK, injects context assembled from focus state, recent memories (fetched from Engram via `internal/engram/client.go`), and seed instructions. Claude calls MCP tools registered in `internal/mcp/tools/register.go` (served by `internal/mcp/server.go` on port 8066). Responses flow back through `internal/effectors/discord.go`. Autonomous wakes fire on a configurable timer (default 2h) and follow the same path, capped by `MaxAutonomousSessionDuration` to enforce coordinator-style sessions.

## Module Map

| Path | Responsibility |
|------|---------------|
| `cmd/bud/main.go` | Daemon entry point — wires all subsystems, handles config, launchd lifecycle |
| `internal/executive/executive_v2.go` | Core orchestrator — runs Claude sessions, manages signal_done, subagents |
| `internal/focus/` | Attention system — salience computation, priority queue, focus/suspend/resume |
| `internal/senses/` | Input adapters — Discord and calendar event ingestion → percepts |
| `internal/effectors/` | Output adapters — Discord message sending and reactions |
| `internal/reflex/` | YAML-defined reflexes — fast-path responses without invoking Claude |
| `internal/mcp/` | MCP HTTP server and all tool registrations (GK, calendar, GitHub, VM, etc.) |
| `internal/memory/` | Short-term working memory — percept pool, threads, traces, inbox |
| `internal/engram/` | HTTP client to the Engram memory service (long-term graph memory) |
| `internal/types/` | Shared type definitions — most-imported package (centrality 28) |
| `internal/integrations/` | External integration helpers (cross-cutting, centrality 16) |
| `internal/budget/` | Token/thinking-time budget tracking across sessions |
| `internal/gtd/` | Local GTD task store (JSON-backed; Things 3 integration via things-mcp MCP server) |
| `internal/eval/` | Memory quality evaluation (judge.go) for self-rating retrieved memories |
| `seed/` | Configuration seeds — guides, plugins, reflexes, wakeup instructions, agent defs |
| `things-mcp/` | Embedded TypeScript MCP server for Things 3 integration (git submodule) |

## Key Files

- `cmd/bud/main.go` — wires all subsystems together; start here to understand initialization order
- `internal/executive/executive_v2.go` — `ExecutiveV2` struct: session lifecycle, context assembly, subagent management
- `internal/focus/attention.go` — salience computation and focus selection logic
- `internal/focus/types.go` — `PendingItem`, `FocusState`, priority levels (`P0`–`P2`)
- `internal/mcp/tools/register.go` — all MCP tool definitions; largest file, entry point for any tool work
- `internal/reflex/engine.go` — YAML reflex loading, evaluation, and action dispatch
- `internal/types/` — shared types imported by nearly every package
- `seed/core.md` → `state/system/core.md` — Claude's identity and standing instructions (seeded on first run)
- `seed/wakeup.md` — injected into autonomous wake prompts
- `docs/architecture/message-flow.md` — sequence diagram of the full request path

## Conventions

- **Testing**: Co-located `*_test.go` files. Go standard testing; run with `go test ./...`. Integration tests in `tests/scenarios/` (YAML-defined). See `docs/testing-playbook.md`.
- **Naming**: Go idioms throughout. Internal packages under `internal/`. Tool names in MCP use snake_case (e.g. `talk_to_user`, `signal_done`).
- **Entry points**: Single binary `bin/bud` built by `scripts/build.sh`. State server `bin/bud-state` is a separate MCP-only binary. `things-mcp` is a TypeScript server built separately.
- **Patterns to know**: Seeds in `seed/` are copied to `state/system/` on first run and treated as live config — edits to `seed/` don't take effect until redeployment or manual copy. Reflexes hot-reload from their YAML files. The `state/` directory is Bud's working directory and is a separate git repo (`bud2/state`).

## Start Here

For a given task type, start at:
- **Adding a new MCP tool**: `internal/mcp/tools/register.go` — all tools are registered here; follow the pattern of an existing tool
- **Modifying reflex behavior**: `seed/reflexes/*.yaml` — YAML rules evaluated by `internal/reflex/engine.go`; hot-reload on change
- **Changing how Claude is prompted**: `internal/executive/executive_v2.go` around `buildPrompt` — context assembly is here; also check `seed/core.md` and `seed/wakeup.md`
- **Adding a new sense/integration**: `internal/senses/` — create a new file following the Discord pattern, wire in `cmd/bud/main.go`
- **Understanding memory retrieval**: `internal/engram/client.go` (HTTP client) + Engram service repo for the storage side
- **Running locally**: `./scripts/build.sh` then `launchctl kickstart -k gui/501/com.bud.daemon`; logs at `~/Library/Logs/bud.log`
