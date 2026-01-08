# Bud Observability System

## Goal

Enable Bud to answer introspection questions like:
- "What did you do today?"
- "Why did you do that?"
- "What happened with X?"
- "Did you handle my message about Y?"
- "What have reflexes been doing?"

## Design Principles

1. **Automatic logging** - Events are captured without Claude explicitly logging
2. **Single source of truth** - One activity log, not scattered across files
3. **Queryable** - Bud can search and filter the log
4. **Human-readable** - Easy to inspect manually if needed
5. **Append-only** - Simple, no complex state management

## Activity Log Structure

**File:** `state/activity.jsonl`

Each line is a JSON object with:

```typescript
interface ActivityEntry {
  ts: string;           // ISO timestamp
  type: ActivityType;   // Event category
  summary: string;      // Human-readable one-liner

  // Optional fields based on type
  source?: string;      // What triggered this (discord, timer, reflex, etc.)
  channel?: string;     // Discord channel if applicable
  thread_id?: string;   // Executive thread if applicable
  intent?: string;      // Reflex intent if applicable
  reasoning?: string;   // Why this happened (for decisions)
  data?: any;           // Structured details
}

type ActivityType =
  | "input"           // Message/event received
  | "reflex"          // Reflex handled something
  | "reflex_pass"     // Reflex passed to executive (gate stopped)
  | "executive_wake"  // Executive started processing
  | "executive_done"  // Executive finished (with summary)
  | "action"          // Action taken (message sent, task updated, etc.)
  | "decision"        // Explicit decision logged by Claude
  | "error";          // Something went wrong
```

## Event Types & What Triggers Them

### 1. Input Events (`type: "input"`)

**Triggered by:** Message received from Discord, scheduled timer, autonomous wake

**Example:**
```json
{"ts":"2025-01-08T10:30:21Z", "type":"input", "summary":"thunder05521: show my todos", "source":"discord", "channel":"1454634002290970761"}
```

**Logged from:** `cmd/bud/main.go` when percept is created from inbox

### 2. Reflex Events (`type: "reflex"`)

**Triggered by:** Reflex successfully handles a query

**Example:**
```json
{"ts":"2025-01-08T10:30:25Z", "type":"reflex", "summary":"Handled GTD query", "intent":"gtd_show_today", "data":{"query":"show my todos", "response":"No tasks for today"}}
```

**Logged from:** `cmd/bud/main.go` after reflex fires successfully

### 3. Reflex Pass Events (`type: "reflex_pass"`)

**Triggered by:** Reflex gate stops, passing to executive

**Example:**
```json
{"ts":"2025-01-08T10:33:19Z", "type":"reflex_pass", "summary":"Not a GTD query, routing to executive", "intent":"not_gtd", "data":{"query":"are you sure?"}}
```

**Logged from:** `cmd/bud/main.go` when reflex gate stops

### 4. Executive Wake Events (`type: "executive_wake"`)

**Triggered by:** Executive starts processing a thread

**Example:**
```json
{"ts":"2025-01-08T10:33:20Z", "type":"executive_wake", "summary":"Processing follow-up question", "thread_id":"t-20260108-103319", "data":{"context":"are you sure?"}}
```

**Logged from:** `internal/executive/executive.go` when session starts

### 5. Executive Done Events (`type: "executive_done"`)

**Triggered by:** Executive completes processing (signal_done or timeout)

**Example:**
```json
{"ts":"2025-01-08T10:33:45Z", "type":"executive_done", "summary":"Verified GTD tasks - confirmed empty", "thread_id":"t-20260108-103319", "data":{"duration_sec":25, "completion":"signal_done"}}
```

**Logged from:** Session completion handler (already have this in signals.jsonl)

### 6. Action Events (`type: "action"`)

**Triggered by:** Bud takes an action (sends message, updates task, etc.)

**Example:**
```json
{"ts":"2025-01-08T10:33:45Z", "type":"action", "summary":"Sent message to discord", "source":"executive", "channel":"1454634002290970761", "data":{"content":"Yes, I double-checked..."}}
```

**Logged from:** `internal/effectors/discord.go` when message sent, or MCP tools when state changes

### 7. Decision Events (`type: "decision"`)

**Triggered by:** Claude explicitly logs reasoning via MCP tool

**Example:**
```json
{"ts":"2025-01-08T10:33:44Z", "type":"decision", "summary":"Decided to verify all GTD buckets", "reasoning":"User asked 'are you sure?' after I said no tasks - should double-check all buckets not just today"}
```

**Logged from:** MCP `activity_log` tool (Claude calls this explicitly for important decisions)

### 8. Error Events (`type: "error"`)

**Triggered by:** Something goes wrong

**Example:**
```json
{"ts":"2025-01-08T10:30:25Z", "type":"error", "summary":"Ollama classification timeout", "data":{"error":"context deadline exceeded", "reflex":"gtd-handler"}}
```

**Logged from:** Error handlers throughout the codebase

## Query Interface

### MCP Tools for Bud to Query Activity

```typescript
// Get recent activity
activity_recent(count: number): ActivityEntry[]

// Get today's activity
activity_today(): ActivityEntry[]

// Search activity by text (searches summary and data)
activity_search(query: string, limit?: number): ActivityEntry[]

// Get activity by type
activity_by_type(type: ActivityType, limit?: number): ActivityEntry[]

// Get activity in time range
activity_range(start: string, end: string): ActivityEntry[]
```

### Example Queries

**"What did you do today?"**
```
→ activity_today()
→ Returns all entries from today, Bud summarizes
```

**"Why did you say X?"**
```
→ activity_search("X")
→ Find the action, look for preceding decision/context
```

**"What have reflexes been handling?"**
```
→ activity_by_type("reflex", 20)
→ Returns recent reflex activity
```

## Implementation Plan

### Phase 1: Core Activity Log

1. Create `internal/activity/activity.go` with:
   - `Log(entry)` - append to file
   - `Recent(n)` - get last n entries
   - `Today()` - get today's entries
   - `Search(query)` - text search

2. Wire up automatic logging in:
   - `cmd/bud/main.go` - input, reflex events
   - `internal/executive/` - executive wake/done
   - `internal/effectors/` - actions

3. Consolidate with existing logs:
   - Merge `signals.jsonl` functionality into activity log
   - Keep `outbox.jsonl` for effector queue (operational, not observability)

### Phase 2: Query Tools

1. Add MCP tools for querying activity
2. Add guide in `state/notes/` for how Bud should use these tools

### Phase 3: Decision Logging

1. Add `activity_log` MCP tool for Claude to log decisions
2. Update Bud's identity to encourage logging important decisions

## Migration

- `signals.jsonl` → entries become `executive_done` type in activity log
- `outbox.jsonl` → keep as operational queue, but also log `action` events
- `journal.jsonl` → replaced by activity log (more automatic, same purpose)

## File Locations

```
state/
├── activity.jsonl      # NEW: unified activity log
├── outbox.jsonl        # KEEP: operational action queue
├── sessions.json       # KEEP: current session state
├── bud_tasks.json      # KEEP: task queue
├── ideas.json          # KEEP: ideas backlog
└── notes/
    └── observability.md  # NEW: guide for Bud on introspection
```

## Open Questions

1. **Retention**: How long to keep activity? Rotate daily? Keep forever?
2. **Size**: Will this get too big? Need pagination for queries?
3. **Privacy**: Any events we should NOT log?
4. **Sync**: Should activity log be git-synced or local-only?
