---
generated_at: 2026-04-09T01:45:00Z
commit: e790de89
repomix: available
---

# bud2 — Overview

> Generated: 2026-04-09 | Commit: e790de89

## Purpose

Bud is a personal AI agent that runs as a macOS launchd daemon (or Linux systemd service), providing autonomous assistance, long-term memory, and task management through a Discord interface. Built in Go, it uses a pluggable LLM provider architecture (`internal/executive/provider/`) to support multiple backends — Claude Code (`claude-code`), OpenCode Serve (`opencode-serve`), and OpenAI-compatible APIs — configured via `internal/config/config.go`. The default provider is `claude-code`. Bud gives each LLM provider an event loop, attention system, and a rich set of MCP tools for interacting with external services and its own state.

## Data Flow

External signals arrive through senses (`internal/senses/discord.go` for Discord messages, `internal/senses/calendar.go` for calendar events). Each sense creates a **percept** in the `PerceptPool` (`internal/memory/percepts.go`) and enqueues a **focus item** into `focus.Queue` (`internal/focus/queue.go`). The `focus.Attention` system (`internal/focus/attention.go`) computes salience (priority × source boost × recency) and selects the highest-priority item. Before reaching the executive, the **reflex engine** (`internal/reflex/engine.go`) evaluates YAML rules from `seed/system/reflexes/` — simple queries are answered directly via Discord without waking Claude.

For items that pass through reflexes, `ExecutiveV2` (`internal/executive/executive_v2.go`) opens an LLM session via the configured provider (`internal/executive/provider/`), injects context assembled from focus state, recent memories (fetched from Engram via `internal/engram/client.go`), and seed instructions. For autonomous wakes, `seed/system/wakeup.md` is injected; for daemon restarts (`impulse:startup`), `seed/system/startup-instructions.md` is injected and memory retrieval is disabled (no recalled context on cold start). Agent definitions and skills are reloaded from plugins on every prompt — changes to plugin configs take effect without a daemon restart. Claude calls MCP tools registered in `internal/mcp/tools/register.go` (served by `internal/mcp/server.go` on port 8066, bound synchronously at startup). Responses flow back through `internal/effectors/discord.go`. After `signal_done`, the executive extracts `<memory_eval>` ratings from the response and calls `RateEngrams()` to feed quality signals back to the Engram memory service. Autonomous wakes fire on a configurable timer (default 2h) and follow the same path, capped by `MaxAutonomousSessionDuration`; when no queued work exists, an idle fallback (doc-maintain) runs via a background subagent.

## Module Map

| Path | Responsibility |
|------|---------------|
| `cmd/bud/main.go` | Daemon entry point — wires all subsystems, handles config, launchd/systemd lifecycle, plugin manifest loading, zettel-libraries generation |
| `internal/config/config.go` | Multi-provider LLM configuration — loads providers, models, API keys from YAML; resolves model roles to provider+model pairs |
| `internal/executive/provider/` | LLM provider abstraction — `Provider` interface with `claude-code` and `opencode-serve` implementations; pluggable session management |
| `internal/executive/executive_v2.go` | Core orchestrator — runs LLM sessions, manages signal_done, subagents, per-prompt agent/skill reload, memory quality feedback |
| `internal/executive/agent_defs.go` | Agent definition loading from plugins; applies tool grants and exclude lists from plugins.yaml |
| `internal/executive/simple_session.go` | Plugin manifest parsing, local-path support, exclude list filtering, zettel-libraries merge (manual entries preserved) |
| `internal/focus/` | Attention system — salience computation, priority queue, focus/suspend/resume |
| `internal/senses/` | Input adapters — Discord and calendar event ingestion → percepts |
| `internal/effectors/` | Output adapters — Discord message sending and reactions |
| `internal/reflex/` | YAML-defined reflexes — fast-path responses without invoking Claude |
| `internal/mcp/` | MCP HTTP server and all tool registrations (GK, calendar, GitHub, VM, etc.) |
| `internal/memory/` | Short-term working memory — percept pool, threads, traces, inbox |
| `internal/engram/` | HTTP client to the Engram memory service (long-term graph memory); includes `RateEngrams()` for quality feedback |
| `internal/types/` | Shared type definitions — most-imported package (centrality 33) |
| `internal/integrations/` | External integration helpers (calendar, GitHub; centrality 16) |
| `internal/budget/` | Token/thinking-time budget tracking across sessions |
| `internal/gtd/` | Local GTD task store (JSON-backed; Things 3 integration via things-mcp MCP server) |
| `seed/system/` | Core bundled plugins (`bud`, `bud-ops`), guides, reflexes, wakeup/startup instructions (reorganized to mirror `state/system/` structure) |
| `state/system/plugins.yaml` | External plugin manifest — lists repos/local paths to load; supports tool_grants and exclude lists |
| `state/system/skill-grants.yaml` | Centralized agent→skill grants — controls which skills each agent type can invoke |

## Key Files

- `cmd/bud/main.go` — wires all subsystems together; start here to understand initialization order and config (statePath defaults to `~/Documents/bud-state`); loads `internal/config/config.go` for LLM provider setup
- `internal/config/config.go` — multi-provider LLM config: declares providers (claude-code, opencode-serve, openai-compatible), model role mapping (e.g. `executive → claude-code/claude-sonnet-4-20250514`), API key resolution from env vars
- `internal/executive/provider/provider.go` — `Provider` and `Session` interfaces that abstract over LLM backends; `opencode_serve.go` and `claude_code.go` are implementations
- `internal/executive/executive_v2.go` — `ExecutiveV2` struct: session lifecycle, context assembly, subagent management, memory quality feedback after signal_done
- `internal/executive/simple_session.go` — plugin manifest parsing; handles `path:` (local), `exclude:`, `tool_grants`, and zettel-libraries generation/merge
- `internal/executive/agent_defs.go` — plugin-aware agent definition loading; applies tool grants from plugins.yaml
- `internal/focus/attention.go` — salience computation and focus selection logic
- `internal/mcp/tools/register.go` — all MCP tool definitions; largest file, entry point for any tool work
- `internal/reflex/engine.go` — YAML reflex loading, evaluation, and action dispatch
- `seed/system/startup-instructions.md` — injected into daemon startup prompts; instructs Claude to check for interrupted subagents and review handoff notes
- `state/system/plugins.yaml` — declares external plugins (GitHub repos or local paths) loaded at startup; supports `exclude:` sub-plugin list

## Conventions

- **Testing**: Co-located `*_test.go` files. Go standard testing; run with `go test ./...`. Integration tests in `tests/scenarios/` (YAML-defined). See `docs/testing-playbook.md`.
- **Naming**: Go idioms throughout. Internal packages under `internal/`. Tool names in MCP use snake_case (e.g. `talk_to_user`, `signal_done`).
- **Entry points**: Single binary `bin/bud` built by `scripts/build.sh`. State server `bin/bud-state` is a separate MCP-only binary. `things-mcp` is a TypeScript server built separately. Binary is codesigned with bud-dev certificate. Runs as launchd (macOS) or systemd (Linux).
- **Patterns to know**: Core plugins (`bud`, `bud-ops`) live in `seed/system/plugins/` and are always bundled. Seed files are organized under `seed/system/` mirroring `state/system/` structure, with drift detection that warns if files diverge. External plugins are declared in `state/system/plugins.yaml` — Bud clones/updates GitHub repos at startup into a local cache (auto-readonly), or loads from a `path:` local checkout. The `exclude:` list on a manifest entry skips named sub-plugin directories (applied in both agent-def loading and MCP server registration). At startup, `generateZettelLibraries` scans all plugin manifests for `"zettels"` declarations and writes `state/system/zettel-libraries.yaml`; manual entries (no `source:` field) are preserved across restarts — only plugin-sourced entries (tagged `source: plugin:<name>`) are refreshed. Agent definitions and skills hot-reload from plugins on every prompt without restart. The `state/` directory is Bud's working directory (`~/Documents/bud-state`) and is a separate git repo. Runtime state files (`gk.db`, `calendar_state.json`, PID) live under `state/system/`. Memory retrieval is disabled for startup impulses; the limit is 6 engrams for regular sessions. After `signal_done`, recalled memory quality ratings are sent back to Engram via `RateEngrams()`. Profiling is disabled by default — enable via `BUD_PROFILE=minimal|detailed|trace`. LLM providers are configured in `state/system/config.yaml`; the default uses `claude-code` provider with `claude-sonnet-4-20250514`. Bud runs as a launchd daemon on macOS and systemd on Linux.

## Start Here

For a given task type, start at:
- **Adding a new MCP tool**: `internal/mcp/tools/register.go` — all tools are registered here; follow the pattern of an existing tool
- **Modifying reflex behavior**: `seed/system/reflexes/*.yaml` — YAML rules evaluated by `internal/reflex/engine.go`; hot-reload on change
- **Changing how the LLM is prompted**: `internal/executive/executive_v2.go` around `buildPrompt` — context assembly is here; also check `seed/system/core.md`, `seed/system/wakeup.md` (autonomous wakes), and `seed/system/startup-instructions.md` (daemon startup)
- **Configuring a different LLM provider**: `internal/config/config.go` — declares providers and model roles; edit `state/system/config.yaml` to switch between `claude-code`, `opencode-serve`, or `openai-compatible` providers
- **Adding/configuring a plugin**: `state/system/plugins.yaml` — add a GitHub repo (`owner/repo`) or local `path:` entry; use `tool_grants` to control which MCP tools the plugin's agents can call, `exclude:` to skip specific sub-plugin directories
- **Changing agent skill access**: `state/system/skill-grants.yaml` — centralized grants file; agent profiles matched by `"namespace:agent"` glob patterns
- **Adding a new sense/integration**: `internal/senses/` — create a new file following the Discord pattern, wire in `cmd/bud/main.go`
- **Understanding memory retrieval**: `internal/engram/client.go` (HTTP client) + Engram service repo for the storage side; quality feedback via `RateEngrams()`
- **Running locally**: `./scripts/build.sh` then `launchctl kickstart -k gui/501/com.bud.daemon` (macOS) or `systemctl --user restart bud` (Linux); logs at `~/Library/Logs/bud.log` (macOS) or journalctl (Linux); state at `~/Documents/bud-state`
