# Bud2: Subsumption-Inspired Agent Architecture

## Influences

### Rodney Brooks' Subsumption Architecture (MIT, 1980s)

Brooks' robots had layered control systems where:
- Each layer is a **complete behavior system**, not a module in a pipeline
- Layers run **concurrently and autonomously**
- Higher layers can **suppress/inhibit** lower layers
- **No central planner** - complex behavior emerges from interaction
- Lower layers handle simpler, more reactive behaviors

Key insight: You don't need a central "brain" orchestrating everything. Simple subsystems doing their thing, with suppression signals, creates complex behavior.

### Biological/Cognitive Systems

Rather than thinking in terms of "agents" communicating, we model **subsystems of a single mind**:

| System | Biological Analog | Function |
|--------|-------------------|----------|
| **Senses** | Sensory organs + early processing | Transform raw signals into percepts (intensity, recency) |
| **Effectors** | Muscles, voice | Execute actions in the world (send messages, API calls) |
| **Reflexes** | Spinal/brainstem reflexes | Fast conditioned responses, no deliberation, can spawn awareness |
| **Attention** | Spotlight of consciousness | Select which thread to focus on based on computed salience |
| **Working Memory** | Prefrontal/parietal workspace | Threads: active (1), paused (few), frozen (many) |
| **Arousal/Drive** | Limbic system, hormones | Modulates attention threshold: urgency, energy |
| **Homeostasis** | Autonomic regulation | Monitor internal state, maintain balance |
| **Consolidation** | Sleep/dreaming | Cull frozen threads, extract learnings, store in long-term |
| **Executive** | Prefrontal cortex | Goals, planning, inhibition, metacognition |

## Core Concepts

### Percepts (automatic properties, no judgment)

Percepts are the output of senses. They have **automatic** properties:

| Property | Meaning | Example |
|----------|---------|---------|
| **Intensity** | Signal strength | User message = high, system notification = low |
| **Recency** | Age | Timestamp, seconds since arrival |
| **Source** | Origin | discord, github, calendar |
| **Type** | Category | message, notification, event |
| **Tags** | Markers | [from:owner], [urgent], [routine] |

**Key insight**: Intensity and recency are like brightness or volume - automatic, no judgment. Salience is computed later, at the thread level.

### Threads (computed salience, involves judgment)

Threads are trains of thought. They live in working memory and have **computed salience**:

| Input | How it affects salience |
|-------|------------------------|
| Percept intensity | High-intensity percepts boost thread |
| Percept recency | Recent percepts boost thread |
| Goal relevance | Matches current focus = boost |
| Source importance | User > system |
| Thread age | Old paused threads decay |

### Percept-Thread Relationship (Many-to-Many)

Percepts don't "belong" to threads. They exist independently in the pool with their automatic properties. Threads **reference** percepts that are relevant to their goal.

```
Percept Pool:                    Thread Pool:
┌─────────────┐                  ┌─────────────────────────┐
│ p-1: "msg"  │◄─────────────────│ Thread A: [p-1, p-3]    │
│ p-2: "notif"│                  │ goal: "respond to user" │
│ p-3: "msg"  │◄────────┐        └─────────────────────────┘
└─────────────┘         │        ┌─────────────────────────┐
      ▲                 └────────│ Thread B: [p-1, p-3]    │
      │                          │ goal: "review PR"       │
      │                          └─────────────────────────┘
      │
  (same percept can be
   relevant to multiple threads)
```

Why many-to-many:
- A message like "check the PR and remind me about the meeting" is relevant to multiple threads
- Threads don't "own" percepts, they just care about them
- Percepts decay/expire based on age, independent of thread assignment
- Simplifies lifecycle - no "unassigned" state needed

### Thread States

| State | In Context? | Description |
|-------|-------------|-------------|
| **Active** | Full context | Currently running (1, maybe few) |
| **Paused** | Listed/summarized | Interrupted, can resume (unlimited) |
| **Frozen** | Just count | Old, awaiting consolidation |
| **Complete** | No | Done, cleaned up |

### Traces (Long-Term Memory)

Traces are consolidated memories - the output of processing percepts into durable knowledge.

| Property | Meaning |
|----------|---------|
| **Content** | Summarized text (LLM-generated from clustered percepts) |
| **Embedding** | Vector for similarity search |
| **Activation** | Current relevance (0-1, decays over time) |
| **Strength** | How reinforced (count of source percepts) |
| **Sources** | Percept IDs this trace was created from |
| **IsCore** | Identity trace (always loaded in context) |
| **IsLabile** | Recently created, subject to reconsolidation |
| **Inhibits** | Trace IDs this trace supersedes |

**Core Traces**: Bootstrapped from `seed/core_seed.md` on first run. Define identity, values, and system knowledge. Always included in executive context.

## Memory Model

### Three Pools

```
Percepts (short-term)     Threads (working memory)     Traces (long-term)
┌──────────────────┐      ┌──────────────────────┐     ┌─────────────────┐
│ Raw inputs       │      │ Active conversations │     │ Consolidated    │
│ Decay in minutes │──────│ Hours to days        │     │ memories        │
│ Have embeddings  │      │ Reference percepts   │     │ Persist forever │
└──────────────────┘      └──────────────────────┘     └─────────────────┘
         │                          │                          ▲
         │                          │                          │
         └──────────────────────────┴──────────────────────────┘
                            Consolidation (every 60s)
```

### Consolidation Process

Every 60 seconds, `Consolidate()` runs:

1. **Gather candidates**: Percepts older than 30 seconds with embeddings, not yet in any trace

2. **Check for reconsolidation**: If percept has correction language ("actually...", "I was wrong...") and matches a labile trace, update the trace in place

3. **Check for reinforcement**: If percept is >80% similar to existing trace, reinforce it (boost strength, update centroid)

4. **Check for inhibition**: If percept matches a labile trace but without correction language, create new trace that inhibits the old one

5. **Cluster remaining**: Group percepts by `conversation_id` (thread ID)

6. **Summarize**: For each cluster, call LLM to summarize the conversation into a trace

```
Cluster: [
  "thunder05521: hey can you help with dinner planning?",
  "Bud: Sure! What are you thinking?",
  "thunder05521: maybe pasta, what do you think?"
]
         ↓ LLM Summarize
Trace: "User asked for help planning dinner, considering pasta as an option"
```

### Spreading Activation

When a new percept arrives, similar traces get "activated":

```go
traces.SpreadActivation(percept.Embedding, boost=0.5, threshold=0.3)
```

This makes related context available to the executive even if it's from old conversations. Activated traces are included in the prompt under "Relevant Memories".

### Trace Lifecycle

```
Percept arrives
     │
     ▼
Spreading activation (boosts similar traces)
     │
     ▼
30+ seconds pass...
     │
     ▼
Consolidation runs
     │
     ├── Similar to existing trace? → Reinforce
     ├── Correction of labile trace? → Reconsolidate (update in place)
     ├── Conflicts with labile trace? → Inhibit (new trace supersedes)
     └── New topic? → Create trace from cluster
     │
     ▼
Trace persists (saved every 60s to traces.json)
```

## Thread Routing

### Association Score

When a percept arrives, we compute an association score with each active thread:

| Signal | Max Contribution | Notes |
|--------|------------------|-------|
| Channel match | 0.30 | Same Discord channel |
| Centroid similarity | 0.30 | Semantic similarity to thread content |
| Topic similarity | 0.20 | Similarity to thread goal |
| Author match | 0.20 | Same user |
| Source match | 0.15 | Same platform |
| Activation level | 0.15 | Thread's current activation |

**Threshold**: Score must be ≥ 0.3 to join existing thread

### Time Decay

Old threads get penalized:

| Thread Age | Decay Multiplier |
|------------|------------------|
| < 1 min | ×1.0 |
| 1-5 min | ×0.8 |
| 5-30 min | ×0.4 |
| > 30 min | ×0.15 |

### Thread Revival

Two mechanisms allow reviving old threads despite time decay:

1. **Back-reference language**: Phrases like "about that...", "as we discussed...", "going back to..." set decay floor to 0.5

2. **High semantic similarity**: If embedding similarity > 0.85, decay floor is 0.5

This allows "hey, about that dinner we were planning" to join an old dinner thread.

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────┐
│  EXECUTIVE                                              │
│  Goals, plans, inhibition, self-reflection              │
│  Model: Opus (expensive, runs periodically + on-demand) │
├─────────────────────────────────────────────────────────┤
│  WORKING MEMORY                                         │
│  ┌─────────────────────────────────────────────────┐    │
│  │ ACTIVE: Thread A (full context loaded)          │    │
│  ├─────────────────────────────────────────────────┤    │
│  │ PAUSED: Thread B, C (listed/summarized)         │    │
│  ├─────────────────────────────────────────────────┤    │
│  │ FROZEN: "7 threads awaiting consolidation"      │    │
│  └─────────────────────────────────────────────────┘    │
├────────────────┬────────────────┬───────────────────────┤
│  ATTENTION     │   AROUSAL      │  HOMEOSTASIS          │
│  Selects       │   (state)      │  Budget, health,      │
│  highest-      │   Modulates    │  context window,      │
│  salience      │   threshold    │  self-monitoring      │
│  thread        │                │                       │
├────────────────┴────────────────┴───────────────────────┤
│  REFLEXES                                               │
│  Pattern → Action (+ optional awareness percept)        │
│  Trained/configured by Executive                        │
│  Model: rules engine, no LLM                            │
├────────────────┬────────────────┬───────────────────────┤
│   SENSE        │   SENSE        │   SENSE               │
│   (Discord)    │   (GitHub)     │   (Calendar)          │
│                │                │                       │
│   → Percepts   │   → Percepts   │   → Percepts          │
│   (intensity,  │   (intensity,  │   (intensity,         │
│    recency)    │    recency)    │    recency)           │
├────────────────┼────────────────┼───────────────────────┤
│   EFFECTOR     │   EFFECTOR     │   EFFECTOR            │
│   (Discord)    │   (GitHub)     │   (Calendar)          │
│                │                │                       │
│   ← Outbox     │   ← Outbox     │   ← Outbox            │
│   send_message │   comment,     │   create_event        │
│   react        │   create_issue │                       │
└────────────────┴────────────────┴───────────────────────┘

        ↕ CONSOLIDATION (offline, runs during idle time) ↕
          Cull frozen threads, extract learnings, store
          Model: Sonnet (batch processing)
```

**Data flow:**
- Senses → percepts.json (pool)
- Threads write → outbox.jsonl
- Effectors read outbox → execute → mark done
- All activity → events.jsonl (audit)

## The Flow

```
Sense (raw input)
  │
  ▼
Percept (intensity, recency) ← automatic, no judgment
  │
  ├──────────────────────────────────────┐
  │                                      ▼
  ▼                              Reflex check
Percept Pool                       │
  │                          MATCH ──→ Execute action
  │                            │            │
  │                            │            ▼
  │                            │      Spawn awareness?
  │                            │        YES → New percept
  │                            │        NO  → (invisible)
  │                            │
  │                       NO MATCH
  │                            │
  ▼                            ▼
Threads scan pool for relevant percepts
  │
  ├── Existing thread finds relevance? → Add percept ref, recompute salience
  ├── New topic detected? → Create new thread with percept ref
  └── No thread cares? → Percept sits in pool, decays naturally
  │
  ▼
Thread salience computed ← judgment happens HERE
  │
  ▼
Attention selects highest-salience THREAD
  │
  ▼
Thread runs until: complete | interrupted | blocked
```

## Reflex System

Reflexes handle simple, predictable queries without waking the executive. They're fast (no LLM) and reduce cognitive load.

### Architecture

```
Message arrives
     │
     ▼
Reflex Engine
     │
     ├── Intent classification (small LLM or rules)
     │        │
     │        ▼
     │   "gtd_show_inbox" / "gtd_add_inbox" / "not_gtd" / etc.
     │        │
     │        ▼
     ├── Pipeline execution
     │   ┌─────────────────────────────────────────────┐
     │   │ Step 1: gate (check conditions)            │
     │   │ Step 2: lookup/transform                   │
     │   │ Step 3: format_response                    │
     │   │ Step 4: reply                              │
     │   └─────────────────────────────────────────────┘
     │        │
     │        ▼
     └── Result: handled | passed_to_executive
```

### Reflex Definition

Reflexes are defined in `state/reflexes/*.yaml`:

```yaml
name: gtd-handler
description: Handle GTD queries (show todos, add to inbox, etc.)
trigger:
  sources: [discord, inbox]
  types: [message]
pipeline:
  - action: llm_classify
    input: $content
    output: intent
    params:
      categories:
        - gtd_show_inbox: "user wants to see inbox items"
        - gtd_add_inbox: "user wants to add something to inbox"
        - not_gtd: "not a GTD-related query"

  - action: gate
    input: $intent
    params:
      stop_if: [not_gtd]  # Pass to executive if not GTD

  - action: gtd_query
    input: $intent
    output: result

  - action: format_response
    input: $result
    output: response

  - action: reply
    input: $response
```

### Query vs Mutation

Reflexes distinguish between:

- **Queries** (read-only): "show my todos", "what's in inbox?" → No trace created
- **Mutations** (state changes): "add X to inbox", "complete task Y" → Creates trace for memory

This prevents memory pollution from repetitive status checks.

### Reflex Log

Recent reflex interactions are kept in a short-term log (not traces) and injected into executive context. This provides continuity without permanent memory.

```
Recent Reflex Activity:
- User asked: "show my todos" → Listed 3 items
- User asked: "add milk to inbox" → Added to inbox
```

### Current Reflexes

| Reflex | Intents | Description |
|--------|---------|-------------|
| gtd-handler | gtd_show_inbox, gtd_add_inbox, gtd_show_today, gtd_show_anytime, gtd_complete, not_gtd | GTD task management |

See `state/notes/reflexes.md` for full documentation.

## Observability

Bud logs all activity to `state/activity.jsonl` for self-inspection.

### Event Types

| Type | When logged |
|------|-------------|
| `input` | Message received from user |
| `reflex` | Query handled by reflex |
| `reflex_pass` | Query passed through reflex to executive |
| `executive_wake` | Executive started processing |
| `executive_done` | Executive finished (with duration) |
| `action` | Outgoing message/reaction sent |
| `decision` | Explicit decision logged via journal_log |
| `error` | Error encountered |

### MCP Tools for Self-Inspection

- `activity_today` - "What did you do today?"
- `activity_recent` - Recent N entries
- `activity_search` - Search by text
- `activity_by_type` - Filter by event type

### Example Query

```
User: "What did you do today?"

Bud calls activity_today(), gets:
[
  {"type": "input", "summary": "thunder05521: show my todos"},
  {"type": "reflex", "summary": "Handled gtd_show_inbox query"},
  {"type": "input", "summary": "thunder05521: add milk to inbox"},
  {"type": "reflex", "summary": "Handled gtd_add_inbox query"},
  ...
]

Bud responds: "Today I handled 12 messages - 8 were GTD queries
handled by reflexes, 4 required executive processing..."
```

See `state/notes/observability.md` for query patterns.

## Design Principles

### 1. Keep Everything Dumb

- Senses: pure code, just parsing and normalization
- Reflexes: rules engine, no LLM needed
- Attention: minimal intelligence (heuristics or tiny model)
- Only Executive uses expensive models (Opus)
- Executive can **suppress** lower layers and handle things itself when needed

### 2. Autonomous Subsystems, Not Central Scheduler

No main loop orchestrating everything. Each subsystem:
- Has its own trigger (event, timer, continuous)
- Runs independently
- Coordinates through shared state (files)

This mirrors biology - no "scheduler" in the brain.

### 3. Communication Via Files

- Simple, inspectable, debuggable
- Journal (append-only log) for events
- State files for current state
- Thread files for working memory
- Exact file structure TBD per subsystem

### 4. Suppression Over Orchestration

Higher layers don't "call" lower layers. They:
- Set suppression flags ("ignore GitHub until tomorrow")
- Adjust attention threshold (via arousal)
- Directly edit reflex rules
- Override by doing things themselves

## Timing

| Subsystem | Trigger | Frequency |
|-----------|---------|-----------|
| Sense (Discord) | Event-driven | Instant (socket) |
| Sense (Calendar) | Polling | Every 15 min |
| Sense (GitHub) | Event-driven | Webhook |
| Reflexes | Pattern match | Instant |
| Attention | Percept arrives / thread changes | Continuous |
| Arousal | State | Updated by events |
| Working Memory | State | Updated by attention |
| Homeostasis | Continuous | Lightweight, always running |
| Executive | Scheduled + summon | Daily + when escalated |
| Consolidation | Idle-triggered | When quiet for N minutes |

## Deep Dives

- [Attention Mechanism](notes/attention.md) - percepts, threads, salience, interruption, consolidation
- [Effectors](notes/effectors.md) - output channels, outbox pattern, action lifecycle
- [Storage Architecture](notes/storage.md) - events.jsonl, mutable state files, proposed structure
- [Memory Architecture](notes/memory.md) - activation-based memory, layers, tools as memory
- [Executive Architecture](notes/executive.md) - tmux + Claude Code, thread types, tools

## Open Questions (Resolved)

### Percept-Thread Matching ✅

**Answer**: Embeddings with multi-signal association scoring.

We compute association scores using:
- Channel match (0.30) - same Discord channel
- Centroid similarity (0.30) - embedding similarity to thread content
- Topic similarity (0.20) - embedding similarity to thread goal
- Author match (0.20) - same user
- Source match (0.15) - same platform
- Activation level (0.15) - thread's current activation

Score ≥ 0.3 joins existing thread, otherwise creates new thread.

See "Thread Routing" section for full details.

### Blocked Threads

**Status**: Not yet implemented.

Planned approach:
- Blocked = waiting for external input or prerequisite
- Tracked via thread state, not separate queue
- Unblocks when relevant percept arrives

### Executive's Role ✅

**Answer**: Executive wakes on escalation from attention.

Current implementation:
- Executive wakes when attention finds no suitable reflex
- Runs in tmux via Claude Code
- Has access to all context (percepts, threads, traces, tools)
- Can create traces, send messages, update state

### Thread Merging

**Status**: Not yet implemented.

When needed, planned approach:
- Inject B's context (summarized) into A
- Mark B as complete with pointer to A
- Percepts stay with original thread (many-to-many allows both to reference them)

## Implementation

**Language: Go**

Why Go:
- Goroutines for concurrent subsystems (senses, attention, effectors all running)
- Single binary deployment
- Good Discord library (`discordgo`)
- "Systems" feel matches subsumption architecture
- No runtime dependencies

**Project Structure:**
```
bud2/
├── cmd/
│   └── bud/main.go       # Entry point, wires subsystems
├── internal/
│   ├── senses/
│   │   └── discord.go    # Discord sense (produces percepts)
│   ├── effectors/
│   │   └── discord.go    # Discord effector (reads outbox)
│   ├── attention/
│   │   └── attention.go  # Thread selection, salience
│   ├── memory/
│   │   ├── percepts.go   # Percept pool
│   │   └── threads.go    # Thread pool
│   └── types/
│       └── types.go      # Percept, Thread, Action types
├── state/                # Runtime state (gitignored)
│   ├── events.jsonl
│   ├── percepts.json
│   ├── threads.json
│   └── outbox.jsonl
├── go.mod
└── go.sum
```

## MCP Communication Architecture

### Context

Claude Code runs in tmux windows (interactive mode) and needs to communicate back to bud2 to send Discord messages. Claude Code supports MCP (Model Context Protocol) for tool integration.

### Decision: File-Based Outbox

We chose to have the MCP server write to the same `outbox.jsonl` file that effectors read from, rather than direct IPC between MCP and effectors.

```
Claude (tmux) → talk_to_user tool → bud-mcp (subprocess)
                                         ↓
                                    outbox.jsonl
                                         ↓
                         Discord effector polls file (100ms)
                                         ↓
                                    Discord API
```

### Alternatives Considered

**1. Direct IPC (Unix socket / HTTP)**
```
Claude → MCP server → socket → effector → Discord
```
- Pros: Lower latency, simpler flow
- Cons: No persistence, lost actions on crash, harder to debug

**2. In-process MCP (HTTP-based)**
```
Claude --mcp-url=http://localhost:9999 → bud2 HTTP handler → in-memory outbox
```
- Pros: Single process, direct memory access
- Cons: Requires HTTP MCP transport (not stdio), more setup

### Why File-Based

| Benefit | Explanation |
|---------|-------------|
| **Persistence** | Actions survive crashes, can retry on restart |
| **Debugging** | Inspect `outbox.jsonl` to see what was sent |
| **Audit trail** | Complete history of all actions |
| **Decoupling** | MCP server doesn't need to know about effectors |
| **Simplicity** | No IPC coordination, just file append |

| Tradeoff | Mitigation |
|----------|------------|
| 100ms polling latency | Acceptable for Discord chat |
| File coordination | Simple append-only, offset tracking |

### Implementation Details

- **MCP server** (`cmd/bud-mcp`): Writes JSON action to `outbox.jsonl`
- **Outbox** (`internal/memory/outbox.go`): Tracks file offset, `Poll()` reads new entries
- **Effector**: Calls `Poll()` before `GetPending()` each cycle

### Future Considerations

If latency becomes critical (e.g., real-time applications), switch to HTTP-based MCP transport where bud2 exposes an HTTP endpoint and Claude connects to it directly. This would allow direct in-memory outbox access while keeping the outbox abstraction for logging.

## Next Steps

1. **Scaffold Go project** - module, basic structure
2. **Define types** - Percept, Thread, Action
3. **Discord sense** - produces percepts from messages
4. **Percept pool** - store/query percepts
5. **Thread pool** - manage thread states
6. **Simple attention** - salience scoring, thread selection
7. **Discord effector** - read outbox, send messages
8. **Iterate**

## Relationship to Bud1

This is a potential **replacement architecture**, not an evolution. The current Bud has:
- Single "agent" brain (Claude via CLI)
- Perch as primitive background process
- File-based memory (this carries over)
- No subsystem separation

Bud2 would be ground-up redesign around the subsumption model.
