# Attention Mechanism

## Core Model: Attention Operates on Threads

Attention doesn't just pick percepts - it picks which **thread** to focus on.

```
┌─────────────────────────────────────────────────────────┐
│  ACTIVE (1, maybe few)     ← in context, running        │
│    Thread A: "respond to user"   salience: 0.8          │
├─────────────────────────────────────────────────────────┤
│  PAUSED (unlimited)        ← listed/summarized          │
│    Thread B: "review PR"         salience: 0.4          │
│    Thread C: "process inbox"     salience: 0.2          │
├─────────────────────────────────────────────────────────┤
│  FROZEN (many, old)        ← just count mentioned       │
│    "7 frozen threads awaiting consolidation"            │
└─────────────────────────────────────────────────────────┘
         ▲
         │ percepts get assigned to threads
         │
┌─────────────────────────────────────────────────────────┐
│  PERCEPT POOL                                           │
│                                                         │
│  [msg.789] intensity: 0.9, recency: 2s   → Thread A     │
│  [notif.1] intensity: 0.3, recency: 45s  → unassigned   │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

## The Full Flow

```
Sense (raw input)
  │
  ▼
Percept (intensity, recency) ← automatic, no judgment
  │
  ▼
Reflex check ─── MATCH ──→ Execute action (automatic, immediate)
  │                              │
  │                              ▼
  │                       Spawn awareness?
  │                         YES → Create percept "I just did X"
  │                         NO  → (done, invisible action)
  │
  NO MATCH
  │
  ▼
Thread assignment
  ├── Related to existing thread? → Assign percept to thread
  ├── New topic? → Create new thread
  └── Noise? → Sit unassigned, decay
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

## Percepts

**What is a percept?**
- Structured output from a sense
- Format varies by sense (that's OK)
- Has automatic properties (no judgment needed)

**Percept properties:**

| Property | Meaning | Example |
|----------|---------|---------|
| **Intensity** | Signal strength | Volume, brightness, urgency markers, user vs system |
| **Recency** | Age | Timestamp, seconds since arrival |
| **Source** | Where it came from | discord, github, calendar |
| **Type** | What kind | message, notification, event |
| **Tags** | Markers | [from:owner], [urgent], [routine] |

**Key insight**: Intensity and recency are **automatic** - like brightness or volume. No judgment or comparison needed. Salience comes later, at the thread level.

**Percept lifecycle:**
1. Created by sense (with intensity, recency, tags)
2. Checked against reflexes
3. Assigned to thread (or not)
4. Processed by thread (or decays)

**Decay:** Unassigned percepts decay. Assigned percepts are held by their thread.

## Threads

**What is a thread?**
- A train of thought / unit of work
- Lives in working memory
- Has state that can be paused/resumed
- Has computed **salience** (this is where judgment happens)

```
Thread {
  id: "thread-abc"
  goal: "respond to user question about X"
  percepts: ["msg.123", "msg.456"]
  state: {
    phase: "drafting response"
    context: { ... }
    next_step: "finish draft and send"
  }
  salience: 0.8  // COMPUTED from percepts + relevance + importance
  status: "active" | "paused" | "frozen" | "complete"
  created_at: timestamp
  last_active: timestamp
}
```

**Thread salience** (computed, involves judgment):

| Input | How it affects salience |
|-------|------------------------|
| Percept intensity | High-intensity percepts boost thread |
| Percept recency | Recent percepts boost thread |
| Goal relevance | Matches current focus = boost |
| Source importance | User > system |
| Arousal state | Modulates threshold, not score |
| Thread age | Old paused threads decay |

**Thread states:**

| State | In Context? | Description |
|-------|-------------|-------------|
| **Active** | Full context | Currently running, attention is here |
| **Paused** | Listed/summarized | Interrupted, can resume |
| **Frozen** | Just counted | Old, awaiting consolidation |
| **Complete** | No | Done, cleaned up |

**Context loading:**
- Active thread: full context loaded
- Paused threads: listed with summaries ("Thread B: reviewing PR #123, paused 5 min ago")
- Frozen threads: just a count ("7 frozen threads")

**Thread lifecycle:**
1. Created (from new percept or spawned by reflex awareness)
2. Active (attention is on this thread)
3. Paused (interrupted by higher-salience thread)
4. Frozen (old, no recent activity)
5. Consolidated (learnings extracted, then deleted)
6. Or: Complete (goal achieved, cleaned up)

## Thread Merging

When two threads turn out to be related:

**Option A: Reassign percepts**
- Move percepts from thread B to thread A
- Delete thread B
- Thread A now has more context

**Option B: Inject context**
- Summarize thread B
- Add summary to thread A's context
- Thread B can be deleted or kept as reference

**Maybe both?**
- Reassign percepts (they belong to A now)
- AND inject B's accumulated context/state (summarized)
- Delete B

This is like: "Oh, these are the same thing. Let me combine what I was thinking."

## Reflexes

**Reflexes are automatic AND can trigger awareness.**

Biological analogy: knee hit → leg jerks (automatic) AND you notice it happened.

```
Reflex {
  pattern: "github.security.*"
  action: "escalate_to_owner"
  spawn_awareness: true
  awareness_percept: "security-alert-handled"
}
```

**Reflex outputs:**
1. **Action** (always): immediate, no reasoning
2. **Awareness** (optional): percept enters pool, may create thread

**Examples:**

| Trigger | Action | Awareness |
|---------|--------|-----------|
| Security alert | Escalate to owner | "I escalated a security alert, should review" |
| User says "thanks" | Send ack emoji | None (invisible, no follow-up needed) |
| Daily backup time | Run backup | "Backup completed" (for logging) |
| Error threshold | Alert + pause | "System unhealthy, paused operations" |

## Arousal

Arousal modulates attention's **threshold**, not salience scores.

```
                    High Arousal          Low Arousal
                    (urgent mode)         (calm mode)
─────────────────────────────────────────────────────────
Threshold           Low                   High
Effect              More things           Only important
                    break through         things break through
Switching           Fast, jumpy           Slow, committed
Mode                Hypervigilant         Deep focus
```

**Arousal factors:**
- Time pressure (deadline approaching)
- Recent errors (something went wrong)
- User waiting (in conversation)
- Resource scarcity (budget low)

## Consolidation

Consolidation is the "sleep" process - runs during idle time.

**Purpose:**
1. Cull old/frozen threads (garbage collection)
2. Extract learnings before culling
3. Store learnings in long-term memory

**Flow:**

```
Frozen threads
    │
    ▼
Select threads to cull (oldest, lowest salience, complete)
    │
    ▼
For each thread:
    │
    ├── Worth learning from?
    │     │
    │     ├── YES → Extract insight
    │     │           │
    │     │           ▼
    │     │         Store in long-term memory
    │     │
    │     └── NO → (skip)
    │
    ▼
Delete thread
```

**What gets extracted?**
- Patterns ("user often asks about X")
- Corrections ("I made mistake Y, learned Z")
- Preferences ("user prefers short responses")
- Facts ("project X uses framework Y")

## Open Questions

1. **Percept-thread matching** - how do we decide if a percept relates to an existing thread? Keywords? Embeddings? Rules?

2. **Blocked threads** - what does "blocked" mean? Waiting for external input? How does it unblock?

3. **Executive's role** - does Executive create threads directly? Or only through reflexes/percepts?

4. **Consolidation trigger** - idle time? Scheduled? Thread count threshold?

5. **Learning extraction** - what model does this? How structured are the learnings?
