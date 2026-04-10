# Agent System Guide

This guide covers the bud2 agent architecture: how agents are defined, loaded, and invoked.

## Two Ways to Run Agents

### `Agent` (sync, in-context)
The built-in Claude Code `Agent` tool spawns a sub-session that runs synchronously within the current context. The parent session waits for the result before continuing.

```
Agent(subagent_type="autopilot-vision:explorer", prompt="Assess this codebase...")
```

Sub-agents specified via `subagent_type` are resolved against the registered `AgentDefinition` map loaded from `state/system/plugins/`. No file management needed.

⚠️ **Warning: executive session time limit.** The executive session has a self-imposed time limit. If an in-context agent runs long (many sequential file writes, large migrations, deep research), the parent session may be killed mid-work, leaving partial results. Use `Agent_spawn_async` for any task requiring more than a few sequential actions.

⚠️ **Warning: shared context window.** The in-context agent's output is injected into the executive's context. Long-running agents with large outputs (file contents, search results) can exhaust the context window. Prefer `Agent_spawn_async` for exploratory or multi-step work.

**Use `Agent` for:** quick lookups, single-file reads, targeted sub-tasks where you need the result immediately to decide next steps.

### `Agent_spawn_async` (async, isolated)
The `Agent_spawn_async` MCP tool spawns an isolated background Claude Code process. The parent session continues immediately and receives a `session_id` for tracking. The subagent reports back via `signal_done`. Use this to delegate longer-running work:

```
Agent_spawn_async(task="...", agent="autopilot-vision:planner")
```

Track progress with `list_subagents`, `get_subagent_status`, `get_subagent_log`.

**Use `Agent_spawn_async` for:** any task requiring >3 sequential actions, file migrations, multi-step implementations, anything that might take more than a minute.

## Plugin Folder Structure

Agents live in `state/system/plugins/<namespace>/agents/`. The seed source is `seed/plugins/<namespace>/agents/`.

```
seed/plugins/
  bud/agents/
    coder.yaml         → bud:coder
    researcher.yaml    → bud:researcher
    reviewer.yaml      → bud:reviewer
    writer.yaml        → bud:writer
  autopilot-vision/agents/
    explorer.md        → autopilot-vision:explorer
    researcher.md      → autopilot-vision:researcher
    planner.md         → autopilot-vision:planner
  autopilot-strategy/agents/
    explorer.md        → autopilot-strategy:explorer
    researcher.md      → autopilot-strategy:researcher
    planner.md         → autopilot-strategy:planner
  autopilot-epic/agents/
    explorer.md        → autopilot-epic:explorer
    planner.md         → autopilot-epic:planner
  autopilot-task/agents/
    explorer.md        → autopilot-task:explorer
    decomposer.md      → autopilot-task:decomposer
    planner.md         → autopilot-task:planner
```

### Naming Convention

Agent keys are always `namespace:agent` — e.g. `bud:coder`, `autopilot-vision:planner`. There are no flat aliases; always use the namespaced form.

## How to Add an Agent

1. Create `seed/plugins/<namespace>/agents/<name>.yaml` or `.md`
2. Use YAML frontmatter:

```yaml
---
name: my-agent
description: Brief purpose description
model: sonnet        # optional: sonnet/opus/haiku (default: inherit)
skills:              # list of skill names from state/system/skills/
  - my-skill
tools:               # extra tools beyond defaults
  - WebSearch
  - mcp__bud2__save_thought
---

## Role

Agent body (markdown). This becomes the system prompt.
```

3. The agent is seeded to `state/system/plugins/` at startup and registered automatically with `WithAgents`. No alias entries needed.

## Agent YAML Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Agent identifier (used as fallback if not set) |
| `description` | string | One-line purpose description |
| `model` | string | Model override: `sonnet`, `opus`, `haiku`, or omit for inherit |
| `skills` | list | Skill names to load from `state/system/skills/` |
| `tools` | list | Additional tools to grant. Use `Agent(ns:name, ...)` syntax for sub-agents (normalized to `Agent` in the definition) |

## Autopilot Cascade

The autopilot planning cascade uses `UP/DOWN/STAY` signals to coordinate across planning levels:

```
autopilot-vision:planner   → UP → autopilot-strategy:planner
autopilot-strategy:planner → UP → autopilot-epic:planner
autopilot-epic:planner     → UP → autopilot-task:planner
autopilot-task:planner     → UP → (done — tasks created in Things)
```

The `handle-subagent-complete` skill handles routing. The entry point is `Agent_spawn_async(agent="autopilot-vision:planner")`.

## Skill Aliases

The `state/system/agent-aliases.yaml` file maps skill aliases. Agent namespace entries have been removed — namespaced resolution is now automatic via the plugins directory. Only skill aliases remain:

```yaml
skills:
  issue-operations: things-operations  # legacy alias
  gk-conventions: gk-conventions
```
