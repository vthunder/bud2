# Bud v2: Memory Architecture

**Created**: 2026-01-12
**Implemented**: 2026-01-13
**Status**: Implemented and active
**Based on**: Research compiled in memory-research.md

> This is the canonical documentation for Bud's v2 memory architecture.
> For v1 historical docs, see `docs/v1/` in the repo root.

---

## Executive Summary

This document describes Bud's v2 architecture based on state-of-the-art research in AI agent memory, attention, and cognitive architectures. The key changes from v1:

1. **Replace thread model** with focus-based single-stream attention
2. **Replace flat traces** with interconnected memory graph
3. **Add conversation buffer** for immediate context
4. **Add entropy filtering** to prevent low-info pollution
5. **Integrate reflex layer** with metacognitive monitoring
6. **Single Claude session** with memory-based context switching

---

## Part 1: Core Architecture

### Overview Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              BUD v2                                         │
│                                                                             │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                     PERCEPTION LAYER                                  │  │
│  │                                                                       │  │
│  │  Discord ─┐                                                           │  │
│  │  Calendar ├──→ Unified Percept Stream ──→ Quality Filter ──→ ───────┐│  │
│  │  GitHub ──┘         (all input)         (entropy score)        ↓    ││  │
│  │                                                                      ││  │
│  │                                          Low-info → Buffer only     ││  │
│  │                                          High-info → Full processing││  │
│  └──────────────────────────────────────────────────────────────────────┘│  │
│                                                                     ↓     │  │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                     REFLEX LAYER (System 1)                           │  │
│  │                                                                       │  │
│  │  Level 0: Pattern Match ──→ Direct action (meeting reminders)        │  │
│  │  Level 1: Local Classify ──→ Intent dispatch (GTD, calendar)         │  │
│  │  Level 2: Mid-tier LLM ────→ Simple generation (acknowledgments)     │  │
│  │                                                                       │  │
│  │  [Proactive Mode Check] ──→ If attending to domain, bypass reflex    │  │
│  │  [Confidence Check] ─────→ Low confidence escalates to Executive     │  │
│  │  [Activity Logged] ──────→ All reflex activity visible to metacog    │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│            ↓ (unhandled or escalated)                                       │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                     ATTENTION LAYER (LIDA-inspired)                   │  │
│  │                                                                       │  │
│  │  Pending Items Queue:                                                 │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐ │  │
│  │  │ P0: Time-critical (reminders, alerts)         [preempt all]    │ │  │
│  │  │ P1: User input (messages from owner)          [high priority]  │ │  │
│  │  │ P2: Due tasks (deadlines, scheduled)          [medium-high]    │ │  │
│  │  │ P3: Active work continuation                  [medium]         │ │  │
│  │  │ P4: Exploration/ideas                         [idle only]      │ │  │
│  │  └─────────────────────────────────────────────────────────────────┘ │  │
│  │                                                                       │  │
│  │  Winner Selection: Highest salience + modifiers → Focus              │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│            ↓ (winning item)                                                 │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                     EXECUTIVE LAYER (System 2)                        │  │
│  │                                                                       │  │
│  │  Single Claude Session                                                │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐ │  │
│  │  │ Context Assembly:                                               │ │  │
│  │  │  1. Core Identity (always)                                      │ │  │
│  │  │  2. Conversation Buffer (recent raw exchanges)                  │ │  │
│  │  │  3. Retrieved Memories (graph-based, focus-relevant)            │ │  │
│  │  │  4. Recent Reflex Activity (observable)                         │ │  │
│  │  │  5. Goal Stack State (current focus, suspended, commitments)    │ │  │
│  │  └─────────────────────────────────────────────────────────────────┘ │  │
│  │                                                                       │  │
│  │  Capabilities:                                                        │  │
│  │  - Multi-step reasoning, planning                                    │  │
│  │  - Tool use (memory, tasks, communication)                           │  │
│  │  - Memory formation and consolidation                                │  │
│  │  - Reflex proposal (knowledge compilation)                           │  │
│  │  - Proactive mode setting (veto reflexes)                            │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│            ↓ (response + updates)                                           │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                     MEMORY LAYER (Three-Tier Graph)                   │  │
│  │                                                                       │  │
│  │  Tier 1: Episode Subgraph (Non-lossy)                                │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐ │  │
│  │  │ - Raw messages with full metadata                               │ │  │
│  │  │ - Reply chain links (what responds to what)                     │ │  │
│  │  │ - Dialogue act tags (backchannel, question, statement)          │ │  │
│  │  │ - Temporal edges (before/after)                                 │ │  │
│  │  │ - Bi-temporal: T (when said) + T' (when ingested)               │ │  │
│  │  └─────────────────────────────────────────────────────────────────┘ │  │
│  │                                                                       │  │
│  │  Tier 2: Entity Subgraph (Extracted)                                 │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐ │  │
│  │  │ - Named entities (people, projects, concepts)                   │ │  │
│  │  │ - Resolved against existing graph (deduplication)               │ │  │
│  │  │ - Bidirectional links to episodes                               │ │  │
│  │  │ - Salience scores (frequency, recency, importance)              │ │  │
│  │  └─────────────────────────────────────────────────────────────────┘ │  │
│  │                                                                       │  │
│  │  Tier 3: Trace Subgraph (Consolidated)                               │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐ │  │
│  │  │ - Summarized memories (1-2 sentences)                           │ │  │
│  │  │ - Links to source episodes and entities                         │ │  │
│  │  │ - Topic/project associations                                    │ │  │
│  │  │ - Activation levels (spreading activation)                      │ │  │
│  │  │ - A-MEM style bidirectional linking between traces              │ │  │
│  │  └─────────────────────────────────────────────────────────────────┘ │  │
│  │                                                                       │  │
│  │  Retrieval: HippoRAG-style PageRank over graph + vector similarity   │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                     GOAL STACK                                        │  │
│  │                                                                       │  │
│  │  Current Focus: What am I doing right now?                           │  │
│  │  Suspended: What was I doing before this interrupt?                  │  │
│  │  Commitments: Tasks I've promised to do                              │  │
│  │  Interests: Ideas I want to explore when idle                        │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                     METACOGNITIVE LAYER                               │  │
│  │                                                                       │  │
│  │  - Observes all activity (reflex + executive)                        │  │
│  │  - Periodic reflection on outcomes                                   │  │
│  │  - Proposes reflex improvements                                      │  │
│  │  - Detects patterns for knowledge compilation                        │  │
│  │  - Runs during idle periods (sleep-time compute)                     │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Part 2: Detailed Component Specifications

### 2.1 Conversation Buffer

**Purpose**: Solve the 60-second percept window problem. Keep recent conversation raw for immediate context.

**Implementation** (LangChain SummaryBufferMemory style):

```
┌──────────────────────────────────────────────────────┐
│              CONVERSATION BUFFER                      │
├──────────────────────────────────────────────────────┤
│ Recent (raw):     Last 5-10 minutes                  │
│                   User messages + Bud responses       │
│                   Full content, no summarization      │
│                                                       │
│ Older (summary):  Beyond 10 minutes                  │
│                   Summarized when buffer overflows    │
│                   Key points preserved                │
├──────────────────────────────────────────────────────┤
│ Scope:            Per-channel (Discord)              │
│                   Or per-focus (autonomous work)      │
├──────────────────────────────────────────────────────┤
│ Reply Tracking:   Each message links to what it      │
│                   responds to (explicit chain)        │
└──────────────────────────────────────────────────────┘
```

**Key insight**: "yes" stays with its question because they're in the same buffer window.

**Incremental Sync Optimization** (implemented 2026-01-13):

Since the Claude session is persistent (single tmux window), we avoid re-sending buffer content that's already in Claude's context:

```
┌─────────────────────────────────────────────────────────┐
│                  BUFFER SYNC LOGIC                       │
├─────────────────────────────────────────────────────────┤
│ session.lastBufferSync = time.Time{}  (zero on start)   │
│                                                          │
│ On first prompt (lastBufferSync is zero):               │
│   - Include summary (if any)                            │
│   - Include ALL buffer entries                          │
│   - Update: session.lastBufferSync = time.Now()         │
│                                                          │
│ On subsequent prompts:                                  │
│   - Include ONLY entries after lastBufferSync           │
│   - Skip summary (already in Claude's context)          │
│   - Update: session.lastBufferSync = time.Now()         │
│                                                          │
│ On session.Reset():                                      │
│   - Clear lastBufferSync (full sync on next prompt)     │
└─────────────────────────────────────────────────────────┘
```

This avoids token waste from re-sending the same conversation history every prompt.

**Additional Filtering** (to avoid redundancy):

```
┌─────────────────────────────────────────────────────────┐
│              BUFFER ENTRY FILTERING                      │
├─────────────────────────────────────────────────────────┤
│ ExcludeID: Current focus item's ID                      │
│   - The user's message appears in "Current Focus"       │
│   - Don't duplicate it in "Recent Conversation"         │
│                                                          │
│ ExcludeBotAuthor: "Bud" (on incremental sync only)      │
│   - Claude already knows what it said in this session   │
│   - Skip bot responses to save tokens                   │
│   - On first sync (session reset): INCLUDE bot msgs     │
│     (Claude has amnesia and needs the history)          │
└─────────────────────────────────────────────────────────┘
```

**Files involved**:
- `internal/executive/simple_session.go`: `lastBufferSync`, `LastBufferSync()`, `UpdateBufferSync()`, `IsFirstPrompt()`
- `internal/buffer/buffer.go`: `BufferFilter`, `GetEntriesSinceFiltered(scope, since, filter)`
- `internal/executive/executive_v2.go`: `buildContext()` uses filtered fetch, `processItem()` updates sync time
- `cmd/bud/main.go`: Sets `BotAuthor: "Bud"` in ExecutiveV2Config

### 2.2 Quality Filter (Entropy-Aware)

**Purpose**: Prevent low-info messages from polluting memory.

**Implementation** (SimpleMem style):

```
Score = α × EntityNovelty + (1-α) × SemanticDivergence

EntityNovelty = |new_entities| / |words|
SemanticDivergence = 1 - cos(embedding, recent_history_embedding)

Threshold: 0.35

Below threshold:
  - Keep in conversation buffer (for context)
  - Don't create episode node
  - Don't trigger consolidation

Above threshold:
  - Full processing
  - Create episode node
  - Queue for consolidation
```

**Dialogue Act Classification** (optional enhancement):
- Detect backchannels ("yes", "ok", "uh-huh")
- Attach to previous turn rather than standalone
- Don't embed in isolation

### 2.3 Attention System

**Purpose**: Replace automatic thread routing with intentional focus selection.

**Implementation** (LIDA Global Workspace inspired):

```python
def select_focus():
    pending = get_pending_items()  # messages, tasks, ideas, interrupts

    for item in pending:
        item.salience = compute_base_salience(item)
        item.salience += apply_modifiers(item)

    # Priority override
    if any(item.priority == P0 for item in pending):
        return highest_salience(filter(lambda x: x.priority == P0, pending))

    # User input wins unless P0 interrupt
    if any(item.type == "user_input" for item in pending):
        return highest_salience(filter(lambda x: x.type == "user_input", pending))

    # Otherwise, highest salience
    return highest_salience(pending)

def compute_base_salience(item):
    if item.type == "user_input": return 1.0
    if item.type == "due_task": return 0.8 + urgency_bonus(item)
    if item.type == "active_work": return 0.6
    if item.type == "idea": return 0.2
    return 0.3

def apply_modifiers(item):
    mod = 0
    if item.from_owner: mod += 0.3
    if item.is_mention or item.is_dm: mod += 0.2
    if relates_to_current_focus(item): mod += 0.1
    return mod
```

### 2.4 Memory Graph

**Purpose**: Replace flat traces with interconnected knowledge structure.

**Three-Tier Architecture** (Zep-inspired):

```
TIER 1: EPISODES (Non-lossy)
├── Node: Episode
│   ├── id: string
│   ├── content: string (raw message)
│   ├── source: string (discord, calendar, etc.)
│   ├── author: string
│   ├── channel: string
│   ├── timestamp_event: datetime (T - when it happened)
│   ├── timestamp_ingested: datetime (T' - when we learned it)
│   ├── dialogue_act: string (backchannel, question, statement, etc.)
│   ├── entropy_score: float
│   └── embedding: vector
│
├── Edge: REPLIES_TO (episode → episode)
├── Edge: FOLLOWS (temporal sequence)
├── Edge: MENTIONS (episode → entity)

TIER 2: ENTITIES (Extracted)
├── Node: Entity
│   ├── id: string
│   ├── name: string (canonical)
│   ├── aliases: []string
│   ├── type: string (person, project, concept, etc.)
│   ├── salience: float
│   └── embedding: vector
│
├── Edge: SAME_AS (entity → entity, for resolution)
├── Edge: RELATED_TO (entity → entity, semantic)
├── Edge: MENTIONED_IN (entity → episode)

TIER 3: TRACES (Consolidated)
├── Node: Trace
│   ├── id: string
│   ├── summary: string (1-2 sentences)
│   ├── topic: string (optional association)
│   ├── activation: float
│   ├── strength: int (reinforcement count)
│   ├── created_at: datetime
│   ├── last_accessed: datetime
│   ├── is_core: bool
│   └── embedding: vector (centroid of sources)
│
├── Edge: SOURCED_FROM (trace → episode)
├── Edge: INVOLVES (trace → entity)
├── Edge: RELATED_TO (trace → trace, A-MEM style)
├── Edge: INVALIDATED_BY (trace → trace, for corrections)
```

**Retrieval Algorithm** (HippoRAG-inspired):

```python
def retrieve_memories(query_embedding, current_focus, limit=10):
    # 1. Seed nodes via vector similarity
    seed_nodes = vector_search(query_embedding, top_k=20)

    # 2. Spread activation through graph
    activated = spread_activation(seed_nodes, hops=2)

    # 3. Apply PageRank-style importance
    ranked = personalized_pagerank(activated, focus=current_focus)

    # 4. Filter and return
    return ranked[:limit]
```

### 2.5 Reflex-Executive Integration

**Purpose**: Make reflexes observable and trainable, enable proactive override.

**Observable Reflexes**:
```
Every reflex execution logs:
- Input that triggered it
- Pattern/classifier that matched
- Actions taken
- Outcome (success/failure)
- Timestamp

Log stored for metacognitive review
Recent log included in Executive context
```

**Proactive Mode**:
```go
type AttentionMode struct {
    Domain      string    // "gtd", "calendar", "all"
    Mode        string    // "bypass_reflex", "debug", "practice"
    SetBy       string    // "executive", "user"
    ExpiresAt   time.Time // auto-expire or manual clear
}

// Before reflex fires:
if attention.IsAttending(reflex.Domain) {
    // Skip reflex, route to executive
    return false
}
```

**Knowledge Compilation** (conscious → automatic):
```
During idle/metacognitive periods:
1. Review executive response logs
2. Detect patterns:
   - Same type of input
   - Same type of response
   - Repeated 3+ times
3. Propose reflex:
   "I notice I always respond to 'good morning' with a greeting.
    Should I create a reflex for this?"
4. If approved, generate YAML and save
```

### 2.6 Single Session Model

**Purpose**: Eliminate multi-session complexity, use memory for context switching.

**Current (problematic)**:
```
Thread A → Session A (tmux window, process, session file)
Thread B → Session B (tmux window, process, session file)
Thread C → Session C (tmux window, process, session file)

Problems: Session corruption, context mixing, complex state management
```

**Proposed**:
```
All input → Single Claude Session
            ├── Context assembled per-focus
            ├── Memory retrieval provides relevant history
            ├── Conversation buffer provides recent raw
            └── Goal stack tracks what was being worked on

Focus switching = Memory retrieval + buffer switch
Not process management
```

---

## Part 3: Data Flow Examples

### Example 1: User says "yes" after a question

**Current behavior (broken)**:
```
1. "yes" arrives as percept
2. RoutePercept() tries to match thread by semantic similarity
3. "yes" matches other "yes"-like things, wrong thread
4. Context is confused
```

**Proposed behavior**:
```
1. "yes" arrives as percept
2. Quality filter: low entropy → buffer only, no episode
3. Attention: user input → high priority → focus
4. Context assembly:
   - Conversation buffer contains the question + "yes"
   - They're together because recent
5. Executive sees: "Q: Should I proceed? A: yes"
6. Responds appropriately
```

### Example 2: Scheduled reminder while user is messaging

**Current behavior**:
```
1. User message arrives → routed to thread A
2. Reminder impulse fires → routed to thread B
3. Two separate sessions, confusion
```

**Proposed behavior**:
```
1. User message arrives → pending queue (P1)
2. Reminder impulse fires → pending queue (P0)
3. Attention: P0 preempts P1
4. Reflex layer: meeting-reminder reflex fires
5. Reminder sent immediately
6. Attention: now P1 is highest
7. User message processed with full context
```

### Example 3: Bud thinks about a topic during idle

**Current behavior**:
```
1. Idle detected → create new thread for thinking
2. Separate session, separate context
3. When user returns, thread switch
```

**Proposed behavior**:
```
1. Idle detected → attention finds highest salience item
2. An idea has salience 0.2, wins because nothing else pending
3. Executive focuses on idea, same session
4. Thinking results saved to memory graph
5. User message arrives → P1 preempts
6. Context: memory retrieval can include the thinking if relevant
7. Bud can say "I was just thinking about X..."
```

---

## Part 4: Migration Path

### Phase 1: Foundation (Low risk)
1. Add conversation buffer alongside current system
2. Add entropy filtering (log-only mode first)
3. Add reply chain tracking to percepts
4. Extend reflex logging for observability

### Phase 2: Memory Graph (Medium risk)
1. Implement three-tier graph structure
2. Migrate existing traces to graph nodes
3. Add entity extraction
4. Implement graph-based retrieval alongside vector

### Phase 3: Attention System (Higher risk)
1. Implement focus-based attention
2. Add proactive mode setting
3. Replace thread routing with attention selection
4. Single session model

### Phase 4: Integration (Refinement)
1. Knowledge compilation mechanism
2. Metacognitive reflection
3. Reflex improvement loop
4. Performance tuning

---

## Part 5: Gap Research Resolution

*Gaps identified 2026-01-12, researched 2026-01-13. Full details in memory-research.md Section 16.*

### 5.1 Conversation Buffer Details ✓ RESOLVED

**Decision**: Hybrid token/time-based retention

```
Buffer Structure:
├── raw_buffer: []Message (last 5-10 min OR ~3000 tokens, whichever first)
├── summary_buffer: string (compressed older context)
├── reply_chain: map[string][]string (messageID → replies)
└── scope: "channel:123" | "focus:task-uuid"
```

**Summarization trigger**: When raw_buffer > 3000 tokens OR oldest message > 10min
**Scope**: Per-channel for Discord, per-focus for autonomous work
**Multi-channel**: Cross-channel links when topics span channels

**Key insight**: "Selective retention over universal compression" - 80-90% token reduction achievable.

### 5.2 Entity Extraction ✓ RESOLVED

**Decision**: Two-tier extraction

| Tier | Method | Speed | When |
|------|--------|-------|------|
| Fast | spaCy/small model | <10ms | Every message |
| Deep | Ollama LLM | ~200ms | Consolidation, ambiguous cases |

**Entity resolution**: Match against graph via embedding similarity (threshold 0.85)
**Aliases**: Track multiple names for same entity

### 5.3 Graph Implementation ✓ RESOLVED

**Decision**: SQLite + custom Go graph layer

**Spreading Activation** (from Synapse paper):
```
u_i(t+1) = (1-δ) * a_i(t) + Σ_{j∈N(i)} S * w_ji * a_j(t) / fan(j)
```
- 3 iterations to stability
- Dual-trigger seeding: BM25 (lexical) + embeddings (semantic)
- Lateral inhibition to suppress weak candidates
- HNSW indexing for fast similarity

**"Feeling of Knowing"**: Reject queries where top-node activation < 0.12 (96.6 F1 adversarial rejection)

**Graph limits**: K=15 max edges per node, ~10k active nodes (archive dormant)

### 5.4 Knowledge Compilation ✓ RESOLVED

**Decision**: Pattern detection with user approval

**Detection algorithm**:
1. Group executive logs by input type + response pattern
2. Identify: 3+ repetitions, 100% success rate, >0.9 response similarity
3. Extract: regex/intent pattern + templated response
4. Propose: "I notice I always respond to X with Y. Create reflex?"

**Approval workflow**: Show examples → User approves/modifies → Generate YAML

### 5.5 Evaluation Metrics ✓ RESOLVED

**Primary metrics** (from LoCoMo + MemoryAgentBench):

| Metric | What It Measures | Target |
|--------|-----------------|--------|
| Reply-Context Accuracy | "yes" finds its question | >95% |
| Retrieval F1 @ k=10 | Right memories retrieved | >0.8 |
| Conflict Resolution | Outdated info updated | HARD: SOTA is 7% multi-hop |
| Entropy Filter FPR | Useful stuff not filtered | <5% |
| Reflex Success Rate | No corrections needed | >90% |

**Key finding**: Conflict Resolution is critical unsolved problem in the field

---

## Part 6: Technology Choices

### Recommended Stack

| Component | Recommendation | Rationale |
|-----------|---------------|-----------|
| Conversation Buffer | In-memory + JSON persistence | Simple, already have pattern |
| Quality Filter | Local embedding + formula | Fast, no external dependency |
| Memory Graph | SQLite + custom graph layer | Embedded, portable, queryable |
| Entity Extraction | Small LLM (Ollama) | Flexible, local |
| Vector Search | Existing Ollama embeddings | Already integrated |
| Graph Algorithms | Custom Go implementation | Control, performance |
| Reflex Layer | Existing YAML + engine | Already working well |
| Executive | Claude API (single session) | Core capability |

### Alternative Considerations

- **Neo4j/DGraph**: If graph queries become complex, consider dedicated graph DB
- **Haiku**: For Level 2 mid-tier generation if needed
- **Fine-tuned classifiers**: For dialogue act detection if accuracy matters

---

## Part 7: Spreading Activation Implementation (2026-01-15)

Based on the Synapse paper (arxiv:2601.02744), implemented full spreading activation algorithm.

### 7.1 Algorithm Parameters

```go
// From internal/graph/activation.go

// Per-iteration decay
DecayRate    = 0.5  // δ - retention factor (1-δ retained per iteration)
SpreadFactor = 0.8  // S - spreading coefficient

// Iterations
DefaultIters = 3    // T - iterations to stability

// Lateral inhibition
InhibitionStrength = 0.15  // β - suppression strength
InhibitionTopM     = 7     // M - number of winners

// Sigmoid transform
SigmoidGamma = 5.0  // γ - steepness
SigmoidTheta = 0.5  // θ - firing threshold

// Seeding thresholds
MinSimilarityThreshold = 0.3  // minimum cosine similarity to seed
SeedBoost = 0.5               // initial activation for seed nodes

// Feeling of Knowing
FoKThreshold = 0.12  // reject if max activation below this
```

### 7.2 Dual-Trigger Seeding

Both triggers contribute seed nodes (union):

1. **Semantic trigger**: Embedding cosine similarity ≥ 0.3
2. **Lexical trigger**: Keyword matching (BM25-style)
   - Tokenize query, filter stop words
   - Match against trace summaries
   - Score by keyword overlap count

### 7.3 Spreading Activation Algorithm

```
For each query:
1. Seed nodes from dual triggers → activation = 0.5
2. For T=3 iterations:
   a. Propagate: contribution = S * weight * activation / fan_out
   b. Self-decay: new_activation = (1-δ) * old_activation
   c. Floor protection: seed nodes min 0.3 (prevents isolated node death)
3. Lateral inhibition: top M=7 suppress competitors
   û_i = max(0, u_i - β * Σ(u_k - u_i)) for u_k > u_i
4. Sigmoid transform: σ(x) = 1/(1+exp(-γ(x-θ)))
5. FoK check: reject if max < 0.12
```

### 7.4 Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| Fresh activation per query | Follows Synapse model; no stale warmth |
| Minimum similarity threshold | Prevents seeding dissimilar traces |
| Seed node floor (0.3) | Prevents isolated nodes from vanishing |
| Top-M inhibition | Winners suppress without threshold artifacts |
| Sigmoid post-processing | Natural bounds, interpretable as probability |

### 7.5 Files Changed

- `internal/graph/activation.go` - Full algorithm implementation
- `internal/executive/executive_v2.go` - Pass query text for dual trigger
- `internal/graph/graph_test.go` - Updated test signatures

---

## Appendix: Research Sources

See `memory-research.md` for full research notes. Key sources:

- [Synapse](https://arxiv.org/abs/2601.02744) - Spreading activation for LLM agents (implemented)
- [MemGPT](https://arxiv.org/abs/2310.08560) - Two-tier memory, self-editing
- [A-MEM](https://arxiv.org/abs/2502.12110) - Zettelkasten-style linking
- [Zep/Graphiti](https://arxiv.org/abs/2501.13956) - Three-tier temporal graph
- [HippoRAG](https://arxiv.org/abs/2405.14831) - PageRank over KG
- [SimpleMem](https://arxiv.org/abs/2601.02553) - Entropy filtering
- [LIDA](https://www.researchgate.net/publication/228621713_LIDA_A_computational_model_of_global_workspace_theory_and_developmental_learning) - Global Workspace Theory
- [Talker-Reasoner](https://arxiv.org/html/2410.08328v1) - Dual-process agents
- [RouteLLM](https://lmsys.org/blog/2024-07-01-routellm/) - Model routing
- [DMC Framework](https://pmc.ncbi.nlm.nih.gov/articles/PMC3289517/) - Proactive/reactive control
