# Attention Mechanism

## Core Model: Attention Operates on Threads

Attention doesn't just pick percepts - it picks which **thread** to focus on.

```
┌─────────────────────────────────────────────────────────┐
│  ACTIVE (1, maybe few)     ← in context, running        │
│    Thread A: "respond to user"   salience: 0.8          │
│              refs: [p-1, p-3]                           │
├─────────────────────────────────────────────────────────┤
│  PAUSED (unlimited)        ← listed/summarized          │
│    Thread B: "review PR"         salience: 0.4          │
│              refs: [p-1, p-2]                           │
│    Thread C: "process inbox"     salience: 0.2          │
├─────────────────────────────────────────────────────────┤
│  FROZEN (many, old)        ← just count mentioned       │
│    "7 frozen threads awaiting consolidation"            │
└─────────────────────────────────────────────────────────┘
         ▲
         │ threads REFERENCE percepts (many-to-many)
         │ same percept can be relevant to multiple threads
         │
┌─────────────────────────────────────────────────────────┐
│  PERCEPT POOL (independent, decays by age)              │
│                                                         │
│  [p-1] intensity: 0.9, recency: 2s    ← Thread A, B     │
│  [p-2] intensity: 0.5, recency: 30s   ← Thread B        │
│  [p-3] intensity: 0.3, recency: 45s   ← Thread A        │
│  [p-4] intensity: 0.2, recency: 2min  ← (no refs yet)   │
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
Threads scan pool for relevant percepts (many-to-many)
  │
  ├── Thread finds relevant percept? → Add ref, recompute salience
  ├── New topic detected? → Create thread with percept ref
  └── No thread cares? → Percept sits in pool, decays by age
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
3. Sits in pool - threads may reference it (many-to-many)
4. Decays naturally based on age (regardless of references)

**Decay:** All percepts decay based on age. Threads hold references, not ownership. When a percept expires, threads lose that reference but keep any derived context/state.

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
  percept_refs: ["p-1", "p-3"]   // references, not ownership (many-to-many)
  state: {
    phase: "drafting response"
    context: { ... }            // accumulated understanding
    next_step: "finish draft and send"
  }
  salience: 0.8  // COMPUTED from referenced percepts + relevance + importance
  status: "active" | "paused" | "frozen" | "complete"
  created_at: timestamp
  last_active: timestamp
}
```

**Note:** Multiple threads can reference the same percept. When computing salience, we look up the current percepts by ID - if a percept has expired, that reference just returns nothing.

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

With many-to-many, merging is simpler:

1. **Copy percept refs** from thread B to thread A (they're just references)
2. **Inject B's context** (summarized state, accumulated understanding)
3. **Delete thread B**

Since percepts exist independently in the pool, there's no "reassignment" - we just add B's refs to A's list. The percepts themselves don't change.

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

1. **Percept relevance detection** - how does a thread decide if a percept is relevant to its goal? Keywords? Embeddings? Rules per sense type?

2. **Blocked threads** - what does "blocked" mean? Waiting for external input? How does it unblock?

3. **Executive's role** - does Executive create threads directly? Or only through reflexes/percepts?

4. **Consolidation trigger** - idle time? Scheduled? Thread count threshold?

5. **Learning extraction** - what model does this? How structured are the learnings?
