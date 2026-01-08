# State Introspection & Management

## Overview

Tools and documentation to inspect, manage, and clean up Bud's internal state. Supports both human use (CLI) and Bud self-introspection (MCP tools).

## Motivation

During development, state accumulates that needs cleanup:
- Test traces from experiments
- Stale percepts/threads
- Corrupted or outdated core memories

Bud also needs to understand its own state for:
- Debugging ("why did you think X?")
- On-demand status reports
- Proposing cleanup of stale data
- Health checks

## Scope

### In Scope (v1)

| Component | File(s) | Operations |
|-----------|---------|------------|
| Traces | `traces.json` | list, show, delete, clear (non-core), clear-core, regen-core |
| Percepts | `percepts.json` | list, count, clear (by age) |
| Threads | `threads.json` | list, show, clear (by status) |
| Logs | `journal.jsonl`, `activity.jsonl` | tail, count, truncate |
| Queues | `inbox.jsonl`, `outbox.jsonl`, `signals.jsonl` | list, count, clear |
| Sessions | `sessions.json` | list, clear stale |

### Out of Scope (v1)

- Automatic triggers (scheduled health checks via reflexes/tasks)
- Proactive garbage collection
- Cross-component queries ("traces without recent percept refs")

## Interface Design

### CLI (`bud state`)

```bash
# Overview
bud state                     # summary of all components
bud state health              # health check with recommendations

# Traces
bud state traces              # list all traces (id, preview, is_core)
bud state traces <id>         # show full trace
bud state traces -d <id>      # delete specific trace
bud state traces --clear      # clear all non-core traces
bud state traces --clear-core # clear core traces (will regenerate)
bud state traces --regen-core # regenerate core from core_seed.md

# Percepts
bud state percepts            # list percepts (id, preview, age)
bud state percepts --count    # just count
bud state percepts --clear    # clear all
bud state percepts --clear --older-than=1h  # clear by age

# Threads
bud state threads             # list threads (id, status, percept count)
bud state threads <id>        # show full thread
bud state threads --clear     # clear all
bud state threads --clear --status=frozen  # clear by status

# Logs
bud state logs                # tail recent journal + activity
bud state logs --truncate=100 # keep last 100 entries each

# Queues
bud state queues              # show inbox/outbox/signals counts
bud state queues --clear      # clear all queues

# Sessions
bud state sessions            # list sessions
bud state sessions --clear    # clear stale sessions
```

### MCP Tools

Mirror CLI functionality for Bud's use:

- `state_summary()` - overview of all components
- `state_health()` - health check with recommendations
- `state_traces(action, id, filter)` - list/show/delete/clear traces
- `state_percepts(action, filter)` - list/count/clear percepts
- `state_threads(action, id, filter)` - list/show/clear threads
- `state_logs(action, count)` - tail/truncate logs
- `state_queues(action)` - list/clear queues
- `state_sessions(action)` - list/clear sessions
- `state_regen_core()` - regenerate core traces from core_seed.md

## Bud Self-Introspection

### Core Trace (always loaded)

```
I can inspect and manage my own state using the state_* MCP tools.
For detailed guidance, read state/notes/state-management.md
```

### Guide File (`state/notes/state-management.md`)

Structure:
1. **When to Introspect** - scenarios that warrant checking state
2. **Tool Quick Reference** - table of common tasks to tools
3. **Cleanup Protocol** - always propose, wait for approval
4. **Safe vs Unsafe Operations** - what's regenerable vs permanent
5. **Example Scenarios** - worked examples

Key principle: Bud proposes deletions but never executes without explicit user consent.

## Implementation

### File Structure

```
cmd/bud/state.go              # CLI subcommand
internal/state/inspect.go     # shared inspection/management logic
internal/mcp/state.go         # MCP tool handlers
state/notes/state-management.md  # Bud's guide
```

### Implementation Order

1. **Core inspection library** (`internal/state/inspect.go`)
   - Functions: ListTraces, DeleteTrace, ClearPercepts, HealthCheck, etc.
   - Shared by CLI and MCP

2. **CLI subcommand** (`cmd/bud/state.go`)
   - Argument parsing, call library, format output
   - Human-friendly tables

3. **MCP tools** (`internal/mcp/state.go`)
   - Wire state_* tools to inspection library
   - JSON responses

4. **Core trace + guide**
   - Add pointer to core_seed.md
   - Write state/notes/state-management.md

5. **Regenerate core** functionality
   - Parse core_seed.md → create traces
   - Clear existing core first

### Dependencies

- Existing state file parsers in `internal/memory/`
- Existing MCP server registration in `internal/mcp/`
- Existing CLI pattern in `cmd/bud/`
