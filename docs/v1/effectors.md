# Effectors (Output Channels)

## The Missing Piece

We focused on input (senses) but output matters too:

| Input | Output |
|-------|--------|
| **Senses** | **Effectors** |
| Eyes, ears | Muscles, voice |
| Discord messages in | Discord messages out |
| GitHub webhooks | GitHub API calls |
| Calendar events | Calendar API writes |

## How Effectors Work

Threads don't call effectors directly. They write to an **outbox**, and effectors pick up actions:

```
Thread (running)
    │
    ▼
"I need to send a Discord message"
    │
    ▼
Write to outbox.jsonl:
{
  id: "action-123"
  effector: "discord"
  type: "send_message"
  payload: { channel: "...", content: "..." }
  timestamp: ISO
  status: "pending"
}
    │
    ▼
Discord Effector (polling or watching outbox)
    │
    ▼
Execute action → Send message
    │
    ▼
Mark action complete (or delete from outbox)
```

## Why Outbox?

1. **Decoupling**: Threads don't need to know how to call Discord API
2. **Retry**: If effector fails, action stays in outbox
3. **Audit**: Can log all actions taken
4. **Rate limiting**: Effector can throttle itself
5. **Batching**: Effector can batch multiple actions

## Effector Types

| Effector | Actions |
|----------|---------|
| **Discord** | send_message, add_reaction, edit_message |
| **GitHub** | create_issue, comment_on_pr, close_issue |
| **Calendar** | create_event, update_event, delete_event |
| **File** | write_file, append_file |
| **Notification** | alert_owner (could be multi-channel) |

## Effector Lifecycle

```
Outbox has pending action
    │
    ▼
Effector picks up action
    │
    ▼
Execute
    │
    ├── SUCCESS → Mark complete, optionally spawn awareness percept
    │
    └── FAILURE → Retry? Mark failed? Alert?
```

## Awareness from Effectors

Like reflexes, effectors can spawn awareness:

```
Effector {
  name: "discord"
  spawn_awareness: true  // for important actions
}
```

After sending a message:
- Create percept: "I sent message X to channel Y"
- This can trigger thread updates, logging, etc.

## Open Questions

1. **Outbox format**: JSONL? SQLite? JSON file with array?
2. **Completion tracking**: Delete from outbox? Mark status? Separate done log?
3. **Failure handling**: Retry count? Dead letter queue?
4. **Priority**: Can some actions jump the queue?
