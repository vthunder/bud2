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

### Thread States

| State | In Context? | Description |
|-------|-------------|-------------|
| **Active** | Full context | Currently running (1, maybe few) |
| **Paused** | Listed/summarized | Interrupted, can resume (unlimited) |
| **Frozen** | Just count | Old, awaiting consolidation |
| **Complete** | No | Done, cleaned up |

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
└────────────────┴────────────────┴───────────────────────┘

        ↕ CONSOLIDATION (offline, runs during idle time) ↕
          Cull frozen threads, extract learnings, store
          Model: Sonnet (batch processing)
```

## The Flow

```
Sense (raw input)
  │
  ▼
Percept (intensity, recency) ← automatic, no judgment
  │
  ▼
Reflex check ─── MATCH ──→ Execute action (automatic)
  │                              │
  │                              ▼
  │                       Spawn awareness?
  │                         YES → New percept "I just did X"
  │                         NO  → (done, invisible)
  │
  NO MATCH
  │
  ▼
Thread assignment
  ├── Related to existing thread? → Assign percept, boost salience
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

## Open Questions

### Percept-Thread Matching

How do we decide if a percept relates to an existing thread?
- Keywords / simple matching?
- Embeddings?
- Rules per sense type?

### Blocked Threads

What does "blocked" mean?
- Waiting for external input?
- Waiting for another thread?
- How does it unblock?

### Executive's Role

Does Executive:
- Create threads directly?
- Only influence through reflexes/arousal/suppression?
- Wake on escalation from attention?

### Thread Merging

When two threads turn out to be related:
- Reassign percepts from B to A?
- Inject B's context (summarized) into A?
- Both?

## Next Steps

1. **Prototype a sense** - Discord (event-driven, familiar)
2. **Define percept format** - intensity, recency, tags
3. **Prototype thread pool** - active/paused/frozen states
4. **Simple attention** - salience scoring, thread selection
5. **Add reflexes** - pattern → action rules
6. **Add consolidation** - cull threads, extract learnings
7. **Iterate**

## Relationship to Bud1

This is a potential **replacement architecture**, not an evolution. The current Bud has:
- Single "agent" brain (Claude via CLI)
- Perch as primitive background process
- File-based memory (this carries over)
- No subsystem separation

Bud2 would be ground-up redesign around the subsumption model.
