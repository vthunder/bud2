# Message Flow Architecture

## Overview

Messages flow from Discord/Calendar senses directly to the processing pipeline via callbacks (no queueing or polling).

## Architecture

### Message Flow Diagram

```
Discord Websocket
       ↓
handleMessage (discord.go)
       ↓
onMessage callback
       ↓
processInboxMessage (main.go)
       ↓
   ┌──────────────┬──────────────┬──────────────┐
   │              │              │              │
   v              v              v              v
signal        impulse        message         thought
   │              │              │              │
   └──────────────┴──────────────┴──────────────┘
                  │
                  v
         processPercept / handleSignal
                  │
                  v
           Executive (focus queue)
```

## Component Responsibilities

### Senses (Discord, Calendar)
- **Input**: External events (websocket messages, calendar polls)
- **Output**: Calls `onMessage(msg *InboxMessage)` callback
- **No buffering**: Messages are processed immediately

### processInboxMessage (main.go)
- **Input**: InboxMessage from senses
- **Routing**:
  - `signal` → handleSignal (session control)
  - `impulse` → processPercept (autonomous work)
  - `message` → ingestToMemoryGraph + processPercept (user input)
  - `thought` → processPercept (bud's own thoughts)
- **No polling**: Executes synchronously when callback is invoked

### processPercept
- **Input**: Percept (converted from InboxMessage)
- **Filtering**: Checks reflexes first (fast path)
- **Output**: Adds to executive focus queue if not handled by reflex

### Executive
- **Input**: Focus queue (separate from message processing)
- **Polling**: 500ms interval to process next focus item
- **Output**: Actions via MCP tools

## Message Types

| Type | Source | Processing | Example |
|------|--------|------------|---------|
| `message` | Discord, synthetic | Ingest to graph + route to executive | User: "help me with X" |
| `signal` | MCP tools (signal_done, memory_reset) | Session control | Done signal after response |
| `impulse` | Autonomous triggers, calendar | Route to executive (may be budget-limited) | Task due, meeting reminder |
| `thought` | MCP save_thought | Store to graph as Bud's episode | save_thought("user prefers mornings") |

## Callbacks

### Senses → Processing
```go
discordSense := NewDiscordSense(cfg, processInboxMessage)
calendarSense := NewCalendarSense(cfg, processInboxMessage)
```

### MCP Tools → Processing
```go
AddThought: func(content string) error {
    msg := &InboxMessage{...}
    processInboxMessage(msg)
    return nil
}

SendSignal: func(signalType, content string, extra map[string]any) error {
    msg := &InboxMessage{Type: "signal", ...}
    processInboxMessage(msg)
    return nil
}
```

## Performance Characteristics

- **Latency**: Sub-millisecond from Discord websocket to processPercept
- **Throughput**: Limited by executive processing (500ms poll), not message ingestion
- **Concurrency**: Discord/Calendar senses run in separate goroutines, callbacks are synchronous

## Removed Components

### Before (v1)
```
Discord → handleMessage → inbox.Add() → [in-memory queue]
                                              ↓
                                        [polling loop @ 100ms]
                                              ↓
                                     inbox.GetPending()
                                              ↓
                                        processPercept
```

### After (v2)
```
Discord → handleMessage → onMessage callback → processPercept
```

**Eliminated**:
- In-memory inbox queue (Inbox struct with Add/GetPending)
- Polling loop (100ms tick checking for pending messages)
- Queue management (MarkProcessed, deduplication)

**Why**: Callbacks provide direct, low-latency message delivery. No need for intermediate buffering since processing is already async via the executive's focus queue.
