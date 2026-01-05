# Storage Architecture

## The Problem

Different data has different access patterns:

| Data | Pattern | Needs |
|------|---------|-------|
| Events (audit) | Append-only | Fast writes, sequential reads |
| Percept pool | Mutable, query | Update decay, find by thread, filter by status |
| Thread pool | Mutable, query | Update status, find active, compute salience |
| Arousal | Single value | Read/write |
| Outbox | Queue | Add, process, remove |

JSONL is great for append-only but bad for mutable state.

## Proposed Structure

```
state/
├── events.jsonl        # Immutable audit trail (append-only)
│
├── percepts.json       # Current percept pool (mutable)
├── threads.json        # Current threads (mutable)
├── arousal.json        # Current arousal state (mutable)
├── reflexes.json       # Reflex rules (mutable, rarely changed)
│
└── outbox.jsonl        # Pending effector actions (append, mark done)
```

## events.jsonl (Audit Trail)

Everything that happened, for debugging and learning:

```jsonl
{"ts":"...","type":"percept_arrived","percept_id":"p-123","source":"discord"}
{"ts":"...","type":"thread_created","thread_id":"t-456","from_percept":"p-123"}
{"ts":"...","type":"thread_paused","thread_id":"t-456","reason":"interrupted"}
{"ts":"...","type":"action_sent","action_id":"a-789","effector":"discord"}
{"ts":"...","type":"action_complete","action_id":"a-789"}
{"ts":"...","type":"reflex_fired","reflex_id":"r-001","percept_id":"p-124"}
```

This is the "memory" that consolidation can learn from.

## percepts.json (Mutable Pool)

Current percepts, updated as they decay or get assigned:

```json
{
  "percepts": [
    {
      "id": "p-123",
      "source": "discord",
      "type": "message",
      "intensity": 0.9,
      "timestamp": "2024-01-05T10:00:00Z",
      "thread_id": "t-456",
      "data": { ... }
    },
    {
      "id": "p-124",
      "source": "github",
      "type": "notification",
      "intensity": 0.3,
      "timestamp": "2024-01-05T09:55:00Z",
      "thread_id": null,
      "data": { ... }
    }
  ]
}
```

**Operations:**
- Add percept
- Assign to thread (`thread_id`)
- Remove (decayed or processed)
- Query by thread_id, source, age

## threads.json (Mutable Pool)

Current threads with state:

```json
{
  "threads": [
    {
      "id": "t-456",
      "goal": "respond to user question",
      "status": "active",
      "salience": 0.8,
      "percepts": ["p-123"],
      "state": {
        "phase": "drafting",
        "context": { ... },
        "next_step": "send response"
      },
      "created_at": "2024-01-05T10:00:00Z",
      "last_active": "2024-01-05T10:01:00Z"
    },
    {
      "id": "t-400",
      "goal": "review PR",
      "status": "paused",
      "salience": 0.4,
      ...
    }
  ]
}
```

**Operations:**
- Create thread
- Update status (active/paused/frozen/complete)
- Update salience
- Add percept reference
- Update state
- Delete (after consolidation)

## arousal.json (Simple State)

```json
{
  "level": 0.5,
  "factors": {
    "user_waiting": false,
    "recent_errors": 0,
    "budget_pressure": false
  },
  "updated_at": "2024-01-05T10:01:00Z"
}
```

## outbox.jsonl (Action Queue)

Pending actions for effectors:

```jsonl
{"id":"a-789","effector":"discord","type":"send_message","payload":{...},"status":"pending","ts":"..."}
{"id":"a-790","effector":"github","type":"comment","payload":{...},"status":"pending","ts":"..."}
{"id":"a-789","status":"complete","completed_at":"..."}
```

Or could be JSON with array, effector removes/marks done.

## Alternative: SQLite

For percepts and threads, SQLite might be better:
- Real queries (find all percepts older than X)
- Transactions (atomic updates)
- Single file

But JSON is:
- Human readable
- Easy to debug
- No dependencies

**Start with JSON, migrate to SQLite if needed.**

## Recency: Computed, Not Stored

Recency = `now - timestamp`

Don't store recency, compute it on read. This means:
- Percepts just need `timestamp`
- "Decay" is just filtering by age
- No need to update percepts over time

## Open Questions

1. **File locking**: Multiple processes reading/writing JSON?
2. **Corruption**: What if write fails mid-way?
3. **Size**: What if percepts.json gets huge? (Consolidation should prevent this)
4. **Outbox cleanup**: Delete completed? Rotate file?
