# Executive Architecture (V1 - SUPERSEDED)

> **⚠️ DEPRECATED**: This document describes the v1 executive with multi-session threading.
> The v2 architecture uses a simplified single-session model:
> - `docs/architecture/v2-memory-architecture.md` - Full v2 design
> - `internal/executive/executive_v2.go` - Implementation
> - `internal/executive/simple_session.go` - Single session manager
>
> Key v2 changes:
> - Single persistent Claude session in tmux (not per-thread)
> - Focus-based context assembly with conversation buffer
> - Incremental buffer sync to avoid re-sending context
> - Graph-based memory retrieval with spreading activation

---

## Overview

The executive is the "thinking" part of bud2 - where Claude (or another LLM) deliberates, plans, and decides on actions. Not all threads need an executive; only **executive threads** invoke the LLM.

## Thread Types

| Type | Runs On | Purpose |
|------|---------|---------|
| **Signal** | Rules/heuristics (no LLM) | Process percept → signal, decide routing |
| **Executive** | Claude via tmux | Deliberate thinking, planning, responding |
| **Reflex** | (not a thread) | Instant pattern→action, stateless |

```
Percept
    │
    ▼
Reflex check ─── MATCH ──→ Instant action (no thread)
    │
    NO MATCH
    │
    ▼
Signal thread (lightweight, rules-based)
    │
    ├── "Needs thinking" → Spawn/feed executive thread
    ├── "Noise" → Drop
    └── "Context update" → Update state, no response
```

## Executive = tmux + Claude Code

Each executive thread gets:
- **tmux window/pane** - visual, debuggable, persistent
- **Claude Code session** - tool use, context management
- **Activated context** - thread goal + percepts + activated memories

### Why tmux?

1. **Visual debugging** - attach to see what Claude is doing
2. **Persistence** - session survives bud2 restarts
3. **Extensibility** - can swap Claude for other CLI-based models
4. **Auxiliary panes** - tail logs, monitor state, etc.

### tmux Layout

```
┌─────────────────────────────────────────────────────────┐
│ tmux session: bud2                                      │
├─────────────────────────────────────────────────────────┤
│ Window 0: monitor                                       │
│   ┌─────────────────────┬─────────────────────────────┐ │
│   │ tail events.jsonl   │ tail percepts/threads       │ │
│   └─────────────────────┴─────────────────────────────┘ │
├─────────────────────────────────────────────────────────┤
│ Window 1: thread-abc (executive)                        │
│   ┌───────────────────────────────────────────────────┐ │
│   │ claude --session thread-abc                       │ │
│   │ ...                                               │ │
│   └───────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────────┤
│ Window 2: thread-def (executive)                        │
│   ┌───────────────────────────────────────────────────┐ │
│   │ claude --session thread-def                       │ │
│   │ ...                                               │ │
│   └───────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

## Executive Lifecycle

```
Thread becomes active (attention selects it)
    │
    ▼
Build context:
  1. Core memories (always)
  2. Activated memories (signal-driven)
  3. Activated tools
  4. Thread goal + state
  5. Relevant percepts
    │
    ▼
Spawn or resume tmux window:
  - New thread → create window, start claude session
  - Existing thread → resume existing window/session
    │
    ▼
Send prompt to Claude (via stdin or session continue)
    │
    ▼
Claude thinks, uses tools:
  - respond_to_user → writes to outbox
  - recall_memory → fetches more context
  - update_state → modifies thread state
  - complete_thread → signals done
    │
    ▼
Effector picks up outbox items
    │
    ▼
Thread pauses (waiting) or completes
```

## Tools

Claude interacts with bud2 via tools. Tools write to shared state (outbox, memory, thread state).

### Core Tools (always loaded)

| Tool | Purpose |
|------|---------|
| `respond_to_user` | Queue message to send via effector |
| `recall_memory` | Search and load more memories |
| `update_thread_state` | Modify thread context/state |
| `complete_thread` | Mark thread as done |
| `pause_for_input` | Signal waiting for external input |

### Domain Tools (activated by context)

| Tool | Activated When |
|------|---------------|
| `send_email` | Email-related signal |
| `create_github_issue` | GitHub-related signal |
| `schedule_event` | Calendar-related signal |
| `read_file` | File/code-related signal |

### Tool Implementation

Tools are implemented as:
1. **MCP server** - bud2 runs MCP server, Claude connects
2. **File-based** - Claude writes to outbox.jsonl, bud2 picks up
3. **Claude Code native** - use built-in tools, parse output

Option 1 (MCP) is cleanest - tools are explicit, typed, documented.

## Claude ↔ bud2 Communication

### bud2 → Claude

- **Initial context**: Written to session, includes activated memories + tools
- **New percepts**: Injected when thread resumes ("new message arrived...")
- **Interrupt**: Send signal to pause/redirect

### Claude → bud2

- **Tool calls**: Via MCP or file writes
- **Completion signals**: Tool call or special output marker
- **State updates**: Via tools

## Session Management

Executive sessions are **persistent** until thread is culled:

```
Thread created → tmux window created
Thread active → Claude session active
Thread paused → tmux window exists, session idle
Thread frozen → window kept for potential resume
Thread culled → window destroyed, session ended
```

### Session Context Accumulation

As thread runs, context accumulates:
- Conversation history
- Tool call results
- Memory retrievals

This is like Claude Code's session management - context builds across interactions.

## Concurrency

Can multiple executive threads run simultaneously?

**Option A: Single active**
- Only one Claude session active at a time
- Simpler, lower cost
- Attention controls which thread runs

**Option B: Multiple concurrent**
- Multiple tmux windows, multiple Claude sessions
- Higher cost, more complex
- Better for parallel tasks

Start with Option A, extend to B if needed.

## Open Questions

1. **Session resume** - How to inject new context into existing session?
2. **Context overflow** - What happens when thread context exceeds limits?
3. **Interruption** - How to interrupt Claude mid-thought?
4. **Cost control** - How to limit executive usage (budget)?
5. **Model selection** - Different models for different thread types?
