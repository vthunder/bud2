# Memory System Research

**Started**: 2026-01-12
**Goal**: Investigate approaches to improve Bud's memory system, particularly around conversation continuity and memory activation quality.

## Current Problems

1. **Memory activation is poor**
   - Low-info messages ("yes", "ok") saved as traces
   - Similarity matching too shallow (semantic similarity without context)
   - Atomic memories unconnected from conversational context

2. **Conversation discontinuity**
   - Replies can be attached to wrong threads
   - Context lost when new Claude session starts
   - "yes" response may not find what it's responding to

## Key Questions to Research

- How do other AI agent systems handle long-term memory?
- What's the state of the art in conversational memory?
- How should episodic vs semantic memory be structured?
- What quality filtering approaches exist for low-info content?
- How do systems track discourse structure / reply chains?
- What consolidation strategies preserve conversational context?

---

## Research Findings

### 2026-01-12: Comprehensive Survey

## 1. Memory Architectures Landscape

### MemGPT (Letta) - Two-Tier Memory
- **Paper**: [MemGPT: Towards LLMs as Operating Systems](https://arxiv.org/abs/2310.08560)
- **Key insight**: Virtual context management inspired by OS memory hierarchy
- **Architecture**: Main context (in-context) + External context (out-of-context)
- **Self-editing**: Agent can update its own persona and user information over time
- **Analogy**: Like human episodic→semantic memory transformation

### A-MEM - Zettelkasten-Style Interconnected Notes (NeurIPS 2025)
- **Paper**: [A-MEM: Agentic Memory for LLM Agents](https://arxiv.org/abs/2502.12110)
- **Key insight**: Atomic notes with bidirectional linking create knowledge networks
- **Architecture**: Each memory is a "note" with:
  - Contextual descriptions
  - Keywords and tags
  - Links to related memories
- **Memory evolution**: New memories can trigger updates to existing memories
- **Results**: 85-93% reduction in token usage vs MemGPT

### Zep/Graphiti - Temporal Knowledge Graph (2025)
- **Paper**: [Zep: A Temporal Knowledge Graph Architecture](https://arxiv.org/abs/2501.13956)
- **Key insight**: Three-tier hierarchical graph with bi-temporal tracking
- **Architecture**:
  1. **Episode subgraph**: Raw input (messages/text/JSON) - non-lossy
  2. **Semantic entity subgraph**: Extracted entities resolved against existing graph
  3. **Community subgraph**: Clustered entities with summaries
- **Bi-temporal model**: Tracks both:
  - T (when event occurred)
  - T' (when we ingested it)
- **Temporal invalidation**: When new info contradicts old, sets t_invalid on old edges
- **Results**: 94.8% on DMR benchmark vs 93.4% MemGPT, 90% latency reduction

### HippoRAG - Neurobiologically Inspired (NeurIPS 2024)
- **Paper**: [HippoRAG](https://arxiv.org/abs/2405.14831)
- **Key insight**: Mimic hippocampal indexing with KG + PersonalizedPageRank
- **Brain→AI mapping**:
  - Neocortex → LLM (knowledge storage)
  - Parahippocampal regions → Retrieval encoders
  - Hippocampus → Knowledge Graph + PageRank
- **Method**: Extract noun phrases/relations, build KG, use PPR for multi-hop retrieval
- **Results**: Up to 20% improvement on multi-hop QA, 10-30x cheaper than iterative methods

### SimpleMem - Semantic Lossless Compression (2026)
- **Paper**: [SimpleMem](https://arxiv.org/abs/2601.02553)
- **Key insight**: Entropy-aware filtering + recursive consolidation
- **Three-stage pipeline**:
  1. **Semantic Structured Compression**: Entropy filtering to discard low-info content
  2. **Recursive Memory Consolidation**: Async clustering of related units
  3. **Adaptive Query-Aware Retrieval**: Dynamic scope based on query complexity
- **Results**: 26.4% F1 improvement over Mem0, 30x token reduction

---

## 2. Quality Filtering & Salience Detection

### SimpleMem's Entropy-Aware Filtering (CRITICAL FOR BUD)
**Formula**: `H(W) = α·|new_entities|/|W| + (1-α)·(1-cos(E(W), E(history)))`

This combines:
- **Entity novelty**: Proportion of newly introduced named entities
- **Semantic divergence**: How different from recent history

**Threshold**: Windows scoring below 0.35 are discarded entirely
- Filters: repetitive logs, non-task-oriented dialogue, low-entropy noise
- Keeps: task-relevant facts, novel information

### Dialogue Act Classification
- **Switchboard DAMSL tagset** includes tags like:
  - `b` = Acknowledge/Backchannel ("yes", "ok", "uh-huh")
  - `sd` = Statement-non-opinion
  - `qy` = Yes-No-Question
- Could use to identify low-info backchannels for special handling
- These are "conversational grease" - important for flow but not for memory

### Entity Salience Scoring
- Position (beginnings are more salient)
- Frequency (repeated = important)
- Grammatical role (subjects > objects)
- Relationships to other entities

---

## 3. Conversation Buffer / Context Management

### LangChain ConversationSummaryBufferMemory
- **Hybrid approach**: Raw recent messages + summarized older ones
- **Trigger**: When token count exceeds threshold, summarize oldest
- **Key benefit**: Preserves both immediate context AND distant memories
- **Directly solves** Bud's 60-second percept window problem

### Sliding Window Approaches
- **ConversationBufferWindowMemory**: Keep last K interactions
- **Challenge**: What if important info is at turn K+1?
- **Solution**: Summary buffer hybrid

### Zep's Conversation Context
- Processes messages in windows of n=4 (two complete turns)
- Extracts entities from current message + preceding 4 messages
- Maintains bidirectional indices (episodes↔entities) for traceability

---

## 4. Reply Chain / Discourse Structure

### The Core Problem
- "yes" is meaningless without knowing what it's responding to
- Semantic similarity finds other "yes"-like things, not the referent
- Current thread association uses time decay (0.15x after 30min)

### Discourse Structure Approaches
- **RST (Rhetorical Structure Theory)**: Tree-based segmentation into discourse units
- **Dialogue disentanglement**: Separate chronological utterances into sessions
- **Speaker-mentioning discourse**: Track who mentions whom

### Anaphora Resolution
- Pronouns ("it", "that", "this") require referent tracking
- Coreference chains link mentions of same entity
- As entities become more salient, references become shorter (full name → pronoun)

### Zep's Approach
- Episodes linked to semantic entities via bidirectional edges
- Enables both forward (artifacts→sources) and backward (episodes→entities) traversal
- Citation traceability maintains conversation structure

---

## 5. Memory Consolidation Strategies

### Sleep-Time Compute (Async Processing)
- **Key insight**: Don't process memory during conversation
- Handle consolidation asynchronously during idle periods
- Improves both response latency AND memory quality
- Bud already does this somewhat (30-second consolidation delay)

### SimpleMem's Recursive Consolidation
**Affinity score**: `ω_ij = β·cos(v_i, v_j) + (1-β)·e^(-λ|t_i-t_j|)`

When affinity > 0.85 threshold:
- Cluster related memory units
- Synthesize into higher-level abstract representation
- Archive fine-grained entries

### LangMem's Approach
- Balance memory creation vs consolidation
- Reconcile new info with existing beliefs
- Either delete/invalidate OR update/consolidate

### Google Memory Bank
- **Memory Extraction**: Extract only meaningful info to persist
- **Memory Consolidation**: Merge new with existing, allowing evolution

---

## 6. Key Insights for Bud

### Problem 1: Low-info messages polluting memory
**Solutions**:
1. **Entropy filtering**: Score messages by (entity novelty + semantic divergence), discard below threshold
2. **Dialogue act classification**: Detect backchannels ("yes", "ok") and don't create standalone traces
3. **Contextual embedding**: Don't embed "yes" alone - embed "Q: want me to proceed? A: yes" as a unit

### Problem 2: Atomic memories without context
**Solutions**:
1. **A-MEM style linking**: Create bidirectional links between related memories
2. **Zep's episode layer**: Keep raw conversation as non-lossy base layer
3. **Conversation pairs**: Store Q-A pairs as units, not individual messages

### Problem 3: Conversation discontinuity
**Solutions**:
1. **Conversation buffer**: Keep recent N turns raw, summarize older
2. **Bi-temporal tracking**: Know when something was said AND when we learned it
3. **Reply chain tracking**: Explicitly track what messages respond to what
4. **Longer context window**: Increase from 60s to 5-10 minutes for raw percepts

### Problem 4: Similarity matching too shallow
**Solutions**:
1. **Graph-based retrieval**: HippoRAG's PageRank over KG instead of pure vector similarity
2. **Multi-view indexing**: Semantic + lexical + structural (SimpleMem)
3. **Context-aware embedding**: Embed with surrounding context, not isolated

---

## 7. Proposed Architecture Changes

### Tier 1: Conversation Buffer (NEW)
- Keep last 5-10 minutes of raw conversation
- Include BOTH user messages AND Bud's responses
- Pass this to Claude as immediate context
- No embedding/similarity matching for this tier

### Tier 2: Episode Layer (ENHANCE percepts)
- Raw messages with metadata
- **Add**: Reply chain tracking (what is this responding to?)
- **Add**: Dialogue act classification (backchannel vs substantive)
- **Add**: Entity novelty score
- Don't consolidate backchannels into traces

### Tier 3: Memory Graph (REPLACE flat traces)
- Nodes: Consolidated memories with rich context
- Edges: Temporal, semantic, and causal links
- Use spreading activation over graph, not just vector similarity
- Consider PageRank-style importance propagation

### Filtering Pipeline (NEW)
1. Message arrives
2. Classify dialogue act (backchannel? question? statement?)
3. If backchannel, attach to previous turn, don't create separate trace
4. Score entropy: entity novelty + semantic divergence
5. Below threshold → don't embed, just keep in buffer
6. Above threshold → proceed to consolidation

---

## Sources

- [MemGPT Paper](https://arxiv.org/abs/2310.08560)
- [A-MEM Paper](https://arxiv.org/abs/2502.12110)
- [Zep Paper](https://arxiv.org/abs/2501.13956)
- [HippoRAG Paper](https://arxiv.org/abs/2405.14831)
- [SimpleMem Paper](https://arxiv.org/abs/2601.02553)
- [LangMem Conceptual Guide](https://langchain-ai.github.io/langmem/concepts/conceptual_guide/)
- [Agent Memory Paper List](https://github.com/Shichun-Liu/Agent-Memory-Paper-List)
- [Pinecone LangChain Memory Guide](https://www.pinecone.io/learn/series/langchain/langchain-conversational-memory/)
- [Dialogue Act Classification (Stanford)](https://web.stanford.edu/~jurafsky/ws97/CL-dialog.pdf)

---

## 8. Deeper Question: Is the Thread Model Right?

### 2026-01-12: Discussion with Owner

The problems identified (conversation discontinuity, wrong thread assignment, session corruption) may be **symptoms of a flawed threading model**, not just implementation bugs.

### Current Thread Model

```
Percept arrives → RoutePercept() → Assign to thread based on:
  - Channel match (0.30 weight)
  - Author match (0.20 weight)
  - Semantic similarity to thread centroid (0.50 weight)
  - Time decay (0.15x after 30 min)

Thread → Claude Session (separate tmux window, process, session file)
```

**Problems:**
1. Routing happens at arrival time, before full context is known
2. Low-info messages ("yes") get routed by semantic similarity, which is meaningless
3. Threads are independent containers but need to share "being Bud"
4. Session management is complex and buggy (corruption, mixing)

### Original Intent of Threads

Owner's design goal:
> "Allow bud to have different trains of thought, or contexts, so that I could have a conversation in one context on topic A, but in another (maybe later) thread bud could do some in-depth thinking about topic B on its own. But at the core, both trains of thought have to 'be' bud in some sense."

This is a valid goal, but **automatic percept routing may not be the right mechanism**.

### Human Analogy: Stream of Consciousness

Humans don't have "threads" - we have:
- **One stream of consciousness** that switches focus
- **Memory** that connects experiences across time
- **Attention** that decides what to focus on
- **Context switches** are intentional, not automatic

We can think about multiple topics, but:
- Not simultaneously in parallel
- By remembering and switching focus
- With memory providing continuity

### Alternative: Focus-Based Model

Instead of automatic percept routing to threads:

```
All input → Single stream → Attention decides focus → Memory provides context

"Threads" become "Focus Areas" or "Topics":
- Not containers for percepts
- More like bookmarks or projects
- Bud decides when to switch focus
- Memory connects related things across focuses
```

**Key differences:**
1. **No automatic routing** - everything goes to Bud, Bud decides
2. **Single Claude session** - context switching via memory, not sessions
3. **Intentional focus** - Bud chooses what to attend to
4. **Topics are retrieval cues** - not containers

### What "Autonomous Thinking" Looks Like

In current model: Bud has a separate thread where it thinks about topic B

In focus model: During idle time, Bud:
1. Recalls something interesting (memory activation)
2. Decides to think about it (attention/salience)
3. Does the thinking (same session, different focus)
4. Saves insights (memory consolidation)

The difference is **intentionality** vs **automatic routing**.

### Open Questions

1. **How does Bud switch focus?**
   - Explicit command ("think about X")?
   - Salience-based (high activation topic gets attention)?
   - Time-based (return to interrupted topics)?

2. **What persists across focus switches?**
   - Core identity (always)
   - Conversation buffer (for current interaction)
   - Activated memories (relevant to current focus)
   - Active tasks/commitments

3. **How does Bud handle interrupts?**
   - User message while thinking about something else
   - Multiple topics in same message
   - Return to previous focus after handling interrupt

4. **What replaces thread state?**
   - Topics/projects as first-class entities?
   - Focus history (what was I working on)?
   - Task queue with context snapshots?

### Possible Architecture

```
┌─────────────────────────────────────────────────────┐
│                    BUD (Single Agent)               │
├─────────────────────────────────────────────────────┤
│  Attention                                          │
│  - What should I focus on right now?                │
│  - User input always wins (interrupt)               │
│  - Otherwise, highest salience topic                │
├─────────────────────────────────────────────────────┤
│  Conversation Buffer                                │
│  - Recent raw exchanges (5-10 min)                  │
│  - Includes Bud's responses                         │
│  - Per-channel or per-topic?                        │
├─────────────────────────────────────────────────────┤
│  Memory Graph                                       │
│  - Episodes (raw, linked)                           │
│  - Semantic entities (extracted, resolved)          │
│  - Traces (consolidated summaries)                  │
│  - All interconnected, temporally aware             │
├─────────────────────────────────────────────────────┤
│  Focus/Topic Registry                               │
│  - Named topics Bud is tracking                     │
│  - Not containers, more like tags/projects          │
│  - Retrieval cues for memory                        │
├─────────────────────────────────────────────────────┤
│  Task Queue                                         │
│  - Commitments Bud has made                         │
│  - Can have associated focus/topic                  │
│  - Triggers attention during idle                   │
└─────────────────────────────────────────────────────┘
```

### Next Steps

1. **Validate the model** - Does this address the original problems?
2. **Design focus switching** - How exactly does attention work?
3. **Design memory retrieval** - How does focus affect what's recalled?
4. **Prototype** - Try single-session with manual focus before full implementation

---

---

## 9. Cognitive Architectures & Attention Models

### 2026-01-12: Deeper Research

### Global Workspace Theory (GWT) & LIDA

**Source**: [LIDA Architecture](https://www.researchgate.net/publication/228621713_LIDA_A_computational_model_of_global_workspace_theory_and_developmental_learning)

GWT is "the most widely accepted, empirically supported theory of the role of consciousness in cognition."

**LIDA's Three-Phase Cognitive Cycle**:

1. **Understanding Phase**
   - Incoming stimuli activate feature detectors
   - Percepts cue episodic and declarative memory
   - Result: "Current Situational Model" - understanding of what's happening now

2. **Consciousness Phase (Attention)**
   - "Attention codelets" scan situational model for salient content
   - Codelets form "coalitions" - temporary alliances advocating for content
   - Coalitions compete; highest activation wins
   - Winning coalition **broadcasts globally** to all modules
   - This is "functional consciousness"

3. **Action Selection Phase**
   - Conscious broadcast triggers memory updates and learning
   - Action schemes compete to be selected
   - Selected behavior executes, completing the cycle

**Key Insight**: Attention is **competitive** - different content competes for the global spotlight. This maps well to Bud's needs: messages, tasks, ideas all compete for attention based on salience.

### SOAR & ACT-R

**SOAR**: Goal-subgoal hierarchy, production rules, unified learning
- Working memory maintains situational awareness
- Decision cycle through goal stack

**ACT-R**: Separate declarative/procedural modules
- Production rules govern processing
- Mirrors human thought patterns

**Comparison**:
| Aspect | SOAR | ACT-R |
|--------|------|-------|
| Memory | Unified goal hierarchy | Separated modules |
| Processing | State-operator-result | Production rules |

### Neural Brain Framework

**Source**: [arxiv:2505.07634](https://arxiv.org/html/2505.07634v1)

- **Top-down attention** modulates perception based on task relevance
- **Active sensing policies** learn to dynamically adjust focus
- **Hierarchical memory**: short-term (immediate), long-term (persistent), contextual (activated by cues)
- **Closed-loop integration**: feedback continuously refines internal models

---

## 10. Ambient Agents & Proactive/Reactive Balance

### Seven Principles of Ambient Agents

**Source**: [Snowplow Blog](https://snowplow.io/blog/seven-principles-of-ambient-agents)

1. **Goal-Oriented**: Clear primary objective gives purpose
2. **Autonomous Operation**: Act without human prompting, within boundaries
3. **Continuous Perception**: Monitor environment, focus on semantically meaningful events
4. **Semantic Reasoning**: Connect observations to goals
5. **Persistence Across Interactions**: Memory of past observations and actions
6. **Multi-Agent Collaboration**: Specialized agents work together
7. **Asynchronous Communication**: Event streams, not point-to-point

**Key Insight**: Ambient agents are both proactive AND reactive. They pursue goals but respond to environmental changes. Constrained autonomy - escalate decisions outside span of control.

### Bud as Ambient Agent

Current Bud already has ambient agent characteristics:
- Always-on (daemon process)
- Event-driven (Discord messages, calendar events, impulses)
- Goal-oriented (tasks, ideas, user requests)

What's missing:
- Unified attention mechanism (currently fragmented across threads)
- Continuous perception without disjointed routing
- Clear span of control / escalation model

---

## 11. Context Engineering Best Practices

### Anthropic's Guidance

**Source**: [Anthropic Engineering Blog](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)

**Core Principle**: "Find the smallest set of high-signal tokens that maximize the likelihood of your desired outcome."

**Strategies**:

1. **Compaction**: Summarize history when approaching limits, preserve architectural decisions
2. **Structured Note-Taking**: External memory (NOTES.md, to-do lists) persists outside context window
3. **Sub-Agent Architectures**: Focused tasks with clean contexts, return condensed summaries
4. **Just-In-Time Loading**: Maintain lightweight identifiers, load data dynamically
5. **Progressive Disclosure**: Agents discover relevant context through exploration

**For Bud**:
- Don't pre-load everything; use memory tools to retrieve on demand
- Conversation buffer = recent raw exchanges
- Traces = condensed summaries for retrieval
- Topics/Focus = lightweight identifiers for context retrieval

### Letta V1 Lessons

**Source**: [Letta Blog](https://www.letta.com/blog/letta-v1-agent)

- Deprecated heartbeats and explicit `send_message` tool
- Now relies on native model reasoning
- Key lesson: Stay "in-distribution relative to the data the LLM was trained on"
- Modern models are trained for agentic patterns; don't fight it

---

## 12. Thought Management & Goal Stacks

### Thought Management System (TMS)

**Source**: [ScienceDirect](https://www.sciencedirect.com/science/article/abs/pii/S1877750325002170)

Inspired by human mind managing 60,000 thoughts per day:
- **Hierarchical goal decomposition**: Break down high-level tasks
- **Self-critique modules**: Evaluate progress, refine decisions
- **Reinforcement learning**: Focus on high-value tasks, eliminate irrelevant ones

### Task Tree Pattern

**Source**: [SuperpoweredAI](https://github.com/SuperpoweredAI/task-tree-agent)

"Most tasks we perform are in service of some larger goal."

Dynamic tree structure:
- Break huge tasks into smaller subtasks
- Continue until actionable
- Core challenge: "getting the right information into the prompt at the right time"

### For Bud's Focus System

Instead of threads as containers, consider:
- **Goal stack**: Current focus + suspended goals
- **Task tree**: Hierarchical decomposition of commitments
- **Salience-based attention**: Compete for focus like LIDA coalitions

---

## 13. Refined Architecture Proposal

Based on all research, here's a more detailed proposal:

### Core Components

```
┌─────────────────────────────────────────────────────────┐
│                 BUD (Single Agent)                      │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │ PERCEPTION                                       │   │
│  │ - Discord senses, Calendar, GitHub, etc.        │   │
│  │ - All input → unified percept stream            │   │
│  │ - NO automatic thread routing                   │   │
│  └─────────────────────────────────────────────────┘   │
│                         ↓                               │
│  ┌─────────────────────────────────────────────────┐   │
│  │ SITUATIONAL MODEL (Working Memory)              │   │
│  │ - Current percepts + activated memories         │   │
│  │ - Conversation buffer (5-10 min raw)            │   │
│  │ - Active goal/task context                      │   │
│  └─────────────────────────────────────────────────┘   │
│                         ↓                               │
│  ┌─────────────────────────────────────────────────┐   │
│  │ ATTENTION (LIDA-inspired)                       │   │
│  │ - Salience scoring for pending items            │   │
│  │ - User input = high salience (interrupt)        │   │
│  │ - Due tasks = elevated salience                 │   │
│  │ - Ideas/exploration = low salience (idle only)  │   │
│  │ - Winning "coalition" gets focus                │   │
│  └─────────────────────────────────────────────────┘   │
│                         ↓                               │
│  ┌─────────────────────────────────────────────────┐   │
│  │ EXECUTIVE (Single Claude Session)               │   │
│  │ - Context = identity + buffer + retrieved mem   │   │
│  │ - Process current focus                         │   │
│  │ - Can switch focus via memory retrieval         │   │
│  │ - Tools for memory, tasks, communication        │   │
│  └─────────────────────────────────────────────────┘   │
│                         ↓                               │
│  ┌─────────────────────────────────────────────────┐   │
│  │ MEMORY GRAPH (Long-term)                        │   │
│  │ - Episodes (raw, linked, temporal)              │   │
│  │ - Entities (extracted, resolved)                │   │
│  │ - Traces (consolidated summaries)               │   │
│  │ - Topics/Projects (retrieval cues, not containers) │
│  │ - All interconnected with edges                 │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │ GOAL STACK                                      │   │
│  │ - Current focus (what am I doing now?)          │   │
│  │ - Suspended goals (what was I doing before?)    │   │
│  │ - Commitments (tasks, promises)                 │   │
│  │ - Interests (ideas for exploration)             │   │
│  └─────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

### Attention/Focus Algorithm

```
Every tick:
  1. Score all pending items by salience:
     - User messages: base 1.0, decay slowly
     - Due tasks: 0.8 + urgency bonus
     - Active work: 0.6 (continuation bias)
     - Ideas: 0.2 (only wins during idle)

  2. Apply modifiers:
     - from:owner → +0.3
     - mention/dm → +0.2
     - relates to current focus → +0.1

  3. Winner determination:
     - If user input pending: always wins (interrupt)
     - Else: highest salience wins
     - Tie-breaker: most recent

  4. Context assembly:
     - Core identity (always)
     - Conversation buffer for winner's context
     - Memory retrieval based on winner's content
     - Goal stack state

  5. Execute:
     - Send to Claude session
     - Handle response
     - Update memory, goals, buffer
```

### What This Solves

| Problem | How Architecture Addresses It |
|---------|------------------------------|
| Low-info messages polluting memory | Entropy filtering before storage |
| Shallow similarity matching | Graph-based retrieval, not just vectors |
| Conversation discontinuity | Buffer + explicit reply tracking |
| Wrong thread assignment | No threads! Single stream with focus |
| Session corruption | Single session, no multi-session complexity |
| "yes" loses context | "yes" in buffer with preceding messages |

---

## Ideas to Explore Further

See `state/ideas.json` for tracked ideas including:
- Zettelkasten-style atomic notes with bidirectional linking
- Temporal knowledge graphs (Zep/Graphiti)
- HippoRAG's PersonalizedPageRank
- Three-tier memory architecture
- Conversation buffer implementation
- Entropy-aware filtering
- Recursive consolidation with affinity clustering
- Dialogue act classification for backchannels
- Focus-based single-stream model
- Global Workspace Theory for attention
- Ambient agent principles
- Talker-Reasoner dual-process architecture

---

## 14. Multi-Model Architecture & Cost Optimization

### 2026-01-12: Research on Tiered Processing

### The Problem

Not everything needs expensive Claude. Current Bud has reflexes using local Ollama, but the question is: how should this fit into the new architecture?

### Model Routing Patterns

**Sources**: [RouteLLM](https://lmsys.org/blog/2024-07-01-routellm/), [AWS Multi-LLM Routing](https://aws.amazon.com/blogs/machine-learning/multi-llm-routing-strategies-for-generative-ai-applications-on-aws/), [Cascade Routing](https://arxiv.org/html/2410.10347v1)

**Core Concept**: "No single model can deliver optimal performance across all tasks."

**Three Approaches**:

1. **Routing**: Classify query → pick one model → done
   - Good when different models excel at different tasks
   - Router can be tiny classifier (BERT) or cheap LLM

2. **Cascading**: Try cheap model first → if uncertain, try bigger model
   - Good when query difficulty varies
   - "Easy" queries handled cheaply

3. **Cascade Routing**: Combine both - route to initial model, then cascade if needed
   - Most flexible, best cost-quality tradeoff
   - Outperforms pure routing or cascading by 1-4%

**Cost Savings**: RouteLLM achieves 85% cost reduction on some benchmarks while maintaining 95% GPT-4 performance.

### Reactive vs Deliberative Agents

**Sources**: [SmythOS](https://smythos.com/developers/agent-development/types-of-agent-architectures/), [GeeksforGeeks](https://www.geeksforgeeks.org/artificial-intelligence/reactive-vs-deliberative-ai-agents/)

**Reactive (Fast Path)**:
- Simple stimulus-response
- No internal world model
- Pattern matching / rule-based
- Low latency, computationally efficient
- Example: Bud's regex reflexes

**Deliberative (Slow Path)**:
- Maintains internal model
- Plans, reasons, predicts outcomes
- Goal-driven decision making
- Higher latency, more capable
- Example: Claude executive

**Hybrid Architecture**:
- Reactive layer handles time-sensitive work
- Deliberative layer handles complex tasks
- "Speed vs smarts, reflex vs reasoning"

### System 1 / System 2 in LLMs

**Sources**: [Talker-Reasoner Paper](https://arxiv.org/html/2410.08328v1), [Meta System 2 Distillation](https://venturebeat.com/ai/meta-researchers-distill-system-2-thinking-into-llms-improving-performance-on-complex-reasoning/)

**Kahneman's Dual Process Theory**:
- **System 1**: Fast, intuitive, automatic (pattern recognition)
- **System 2**: Slow, deliberate, analytical (complex reasoning)

**Talker-Reasoner Architecture**:

```
┌─────────────────────────────────────────────────────┐
│                  SHARED MEMORY                      │
│  - Belief states (JSON)                             │
│  - Interaction history                              │
│  - Tool results                                     │
└─────────────────────────────────────────────────────┘
        ↑                              ↑
        │ reads                        │ writes
        │                              │
┌───────────────────┐    ┌─────────────────────────────┐
│   TALKER          │    │   REASONER                  │
│   (System 1)      │    │   (System 2)                │
│                   │    │                             │
│   - Fast LLM      │    │   - Slow/capable LLM        │
│   - Low latency   │    │   - Multi-step planning     │
│   - Pattern match │    │   - Tool invocation         │
│   - Cached beliefs│    │   - Belief formation        │
└───────────────────┘    └─────────────────────────────┘
```

**Key Insight**: Communication via shared memory, not synchronous calls. Talker can operate with slightly stale beliefs to minimize latency.

**Conditional Override**: For complex tasks, Talker waits for Reasoner. For simple tasks, Talker proceeds with cached beliefs.

### Bud's Current Reflex System

**What it does well**:
- Regex pattern matching (no LLM needed)
- Local Ollama classification (qwen2.5:7b)
- Bypasses expensive executive for simple tasks
- Pipeline of actions (fetch, reply, gtd operations)

**Example: gtd-handler**:
```yaml
trigger:
  source: inbox
  classifier: ollama  # cheap local model
  intents: [gtd_show_today, gtd_add_inbox, ...]
pipeline:
  - action: gate
    condition: "{{.intent}} == not_gtd"
    stop: true  # fall through to executive
  - action: gtd_dispatch
  - action: reply
```

**Example: meeting-reminder**:
```yaml
trigger:
  source: impulse:meeting_reminder
  classifier: none  # pure pattern match
pipeline:
  - action: reply
    message: "Heads up - {{.event_title}} starts in {{.time_until}}..."
```

### How Reflexes Fit the New Architecture

The reflex system is essentially **System 1 / Talker** functionality:
- Fast, pattern-based responses
- Local LLM for classification
- No deep reasoning required

**Proposed Integration**:

```
Input arrives
    ↓
┌─────────────────────────────────────────────────────┐
│ REFLEX LAYER (System 1)                             │
│                                                     │
│ Level 0: Pattern Match (regex, source filter)      │
│   → Direct action, no LLM                           │
│   → Examples: meeting reminders, state sync         │
│                                                     │
│ Level 1: Local Classification (Ollama)             │
│   → Cheap LLM intent detection                      │
│   → Examples: GTD queries, calendar queries         │
│                                                     │
│ Level 2: Mid-tier LLM (Haiku, local 7B)            │
│   → Simple generation, summaries                    │
│   → Examples: Quick acknowledgments, formatting     │
│                                                     │
│ If handled → respond, update memory, done           │
│ If not handled → fall through                       │
└─────────────────────────────────────────────────────┘
    ↓ (complex/uncertain queries)
┌─────────────────────────────────────────────────────┐
│ EXECUTIVE LAYER (System 2)                          │
│                                                     │
│ - Full Claude session                               │
│ - Multi-step reasoning                              │
│ - Tool use, planning                                │
│ - Memory formation                                  │
│ - Belief updates                                    │
└─────────────────────────────────────────────────────┘
```

### Attention Priority with Impulses

Per owner's note: Some impulses should have highest priority, even interrupting user messages.

**Proposed Priority Levels**:
```
Priority 0 (Highest): Time-critical interrupts
  - Scheduled reminders
  - Urgent notifications
  - System alerts
  → Preempt everything, even user messages

Priority 1: User input
  - Direct messages from owner
  - Mentions
  → High priority, but can be preempted by P0

Priority 2: Due tasks
  - Tasks hitting deadline
  - Scheduled autonomous work
  → Medium-high, yields to user

Priority 3: Active work continuation
  - Continue previous thread
  - Background processing
  → Medium, yields to new input

Priority 4: Exploration/ideas
  - Idle thinking
  - Curiosity-driven exploration
  → Only wins when nothing else pending
```

### Cost Optimization Strategy

**Current Bud Costs**:
- Reflexes: ~free (local Ollama, regex)
- Executive: Expensive (Claude API)

**Optimized Approach**:
1. **Maximize Level 0**: More patterns for common cases
2. **Expand Level 1**: Train local classifiers for common intents
3. **Add Level 2**: Use Haiku or local models for simple generation
4. **Reserve Claude**: Only for complex reasoning, planning, novel situations

**Decision Criteria for Routing**:
- Is there a pattern match? → Level 0
- Is it a known intent category? → Level 1
- Is it simple generation (acknowledgment, formatting)? → Level 2
- Does it require reasoning, planning, or novel response? → Executive

### Open Questions

1. **Where does reflex logic live in single-session model?**
   - Before Claude session? (current approach)
   - As tools within Claude session?
   - Hybrid?

2. **How does mid-tier (Level 2) work?**
   - Separate Haiku session?
   - Local model?
   - Part of reflex pipeline?

3. **How do we detect "uncertainty" for cascade?**
   - Confidence scores from classifier?
   - Heuristics (question marks, complexity indicators)?
   - Let cheap model try, check quality?

4. **Should Reasoner run asynchronously?**
   - Like Talker-Reasoner: Reflex responds immediately, Executive thinks in background
   - Or: Wait for Executive when needed?

---

## Ideas to Explore Further

See `state/ideas.json` for tracked ideas including:
- Zettelkasten-style atomic notes with bidirectional linking
- Temporal knowledge graphs (Zep/Graphiti)
- HippoRAG's PersonalizedPageRank
- Three-tier memory architecture
- Conversation buffer implementation
- Entropy-aware filtering
- Recursive consolidation with affinity clustering
- Dialogue act classification for backchannels
- Focus-based single-stream model
- Global Workspace Theory for attention
- Ambient agent principles
- Talker-Reasoner dual-process architecture
- RouteLLM / cascade routing for cost optimization

---

## 15. Conscious-Automatic Integration: Different Architectural Models

### 2026-01-12: Deeper Research on the Tension

### The Core Problem

In Bud's current reflex system, **lower layers decide whether to escalate**. The conscious layer (Claude) may never see what reflexes handle. This is different from human cognition where:

1. You can consciously attend to normally automatic behavior (throw a baseball deliberately)
2. Conscious practice trains automatic behavior (repetition → skill)
3. Consciousness can veto prepared automatic actions (stop yourself)
4. You can observe your own automatic responses (notice a habit)

### Different Architectural Models

**Sources**: [MIDCA](https://aaai.org/papers/9886-midca-a-metacognitive-integrated-dual-cycle-architecture-for-self-regulated-autonomy/), [LIDA](https://www.researchgate.net/publication/228621713_LIDA_A_computational_model_of_global_workspace_theory_and_developmental_learning), [ACT-R](https://www.scotthyoung.com/blog/2022/02/15/act-r/), [Subsumption](https://en.wikipedia.org/wiki/Subsumption_architecture), [Reflexion](https://www.promptingguide.ai/techniques/reflexion)

#### Model A: Opaque Automatic Layer (Bud's Current)

```
Input → Reflex Layer → [handles or falls through]
                ↓
        Executive Layer (only sees what falls through)
```

**Properties**:
- Lower layer decides to handle or escalate
- Conscious layer doesn't see what automatic layer handles
- No conscious training of automatic behavior
- Efficient but not integrated

**Problem**: "Completely invisible to the conscious layer, perhaps only emerging later as memories" - per owner observation.

#### Model B: Observable Automatic Layer (MIDCA, LIDA)

```
Input → Reflex Layer → action
              ↓ (trace)
        Metacognitive Layer (observes all activity)
```

**MIDCA's Dual-Cycle**:
- Cognitive cycle: perception → interpretation → goal evaluation → intention → planning → action
- Metacognitive cycle: monitoring → control → learning
- Meta-layer "declaratively represents and monitors traces of cognitive activity"

**LIDA's Conscious Learning Hypothesis**:
- "All significant learning requires consciousness"
- Global broadcast reaches all memory systems
- Procedural memory learns from conscious content
- Even automatic actions can trigger attention if anomalous

**Properties**:
- All activity visible to metacognitive layer
- Can learn from observing automatic handling
- Can detect when automatic behavior is suboptimal
- Supports introspective learning

#### Model C: Controllable Automatic Layer (Subsumption, Veto)

```
Input → Reflex Layer ←──┐
              ↓         │ (suppress/override)
        Executive Layer ─┘
```

**Subsumption Architecture** (Brooks):
- Hierarchical layers, higher can subsume lower
- Higher layers can suppress inputs or inhibit outputs of lower layers
- "Each layer implements a particular level of behavioral competence"

**Veto/Intentional Inhibition**:
- Research shows dorsal fronto-medial cortex (dFMC) can veto prepared actions
- "Refraining voluntarily from already planned behavior, by a final intervention"
- Conscious attention can override automatic response

**Properties**:
- Conscious layer can take over from automatic
- Can deliberately practice normally-automatic behavior
- Control is bidirectional

#### Model D: Reflective Learning (Reflexion, INoT)

```
Input → Action → Outcome → Reflection → Update Rules
                              ↓
                        Better behavior next time
```

**Reflexion Framework**:
- After action, reflect on outcome
- Convert environmental feedback into self-reflection
- Update behavior based on reflection
- "Agents that can self-reflect on their own mistakes... learn to avoid similar mistakes"

**Introspection of Thought (INoT)**:
- Virtual multi-agent debate within single LLM
- Self-denial and reflection occur internally
- Reduces need for external iteration

**Properties**:
- Learning happens through reflection on outcomes
- Can improve both automatic and deliberate behavior
- Gradual improvement through self-critique

### ACT-R Knowledge Compilation

**How conscious becomes automatic**:

1. **Declarative Stage**: Conscious knowledge of facts/rules
   - "Throw ball by extending arm, releasing at 45°"
   - Slow, effortful, requires attention

2. **Knowledge Compilation**: Through practice
   - Composition: collapse sequences into single productions
   - Proceduralization: embed factual knowledge into procedures

3. **Procedural Stage**: Automatic execution
   - No longer requires conscious attention
   - Fast, fluent, can run in parallel with other tasks

**Key insight**: "Conversion of declarative knowledge into procedural knowledge is crucial for learning complex skills... takes a very long time and requires a lot of good practice."

### What This Means for Bud

Current Bud is **Model A** (Opaque). The reflex layer is invisible to Claude unless:
- A reflex logs its activity (but Claude doesn't see this in real-time)
- The reflex fails and falls through
- Memory consolidation captures reflex outcomes later

**Options for integration**:

1. **Make reflexes observable** (Model B)
   - Log all reflex activity to a visible trace
   - Include in Claude's context: "Recent reflex activity: ..."
   - Already partially implemented! (buildPrompt includes reflex logs)

2. **Allow conscious override** (Model C)
   - Before reflex fires, check if conscious attention is engaged
   - If Claude is "attending" to this input type, bypass reflex
   - Enable deliberate practice mode

3. **Enable conscious reflex training** (Knowledge Compilation)
   - After Claude handles something deliberately, check if it's pattern-like
   - Propose: "I notice I handle X the same way each time. Should I make this a reflex?"
   - Claude writes the reflex rule consciously

4. **Reflective improvement** (Model D)
   - Periodically review reflex outcomes
   - Identify reflexes that produce poor results
   - Modify or disable problematic reflexes

### Proposed Hybrid Architecture

```
┌─────────────────────────────────────────────────────────┐
│                METACOGNITIVE LAYER                      │
│                                                         │
│  - Observes all activity (reflex and executive)         │
│  - Can veto/override reflexes when attending            │
│  - Proposes new reflexes from repeated patterns         │
│  - Reviews and updates reflexes based on outcomes       │
└─────────────────────────────────────────────────────────┘
         ↑ observes        ↓ controls
┌─────────────────────────────────────────────────────────┐
│                                                         │
│  REFLEX LAYER              EXECUTIVE LAYER              │
│  (System 1)                (System 2)                   │
│                                                         │
│  - Pattern match      ←→   - Full reasoning             │
│  - Local LLM classify      - Tool use                   │
│  - Quick action            - Planning                   │
│                                                         │
│  [Visible to meta]         [Can override reflexes]      │
│  [Can be trained]          [Can propose new reflexes]   │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

### Key Differences from Current Architecture

| Aspect | Current | Proposed |
|--------|---------|----------|
| Reflex visibility | Opaque (logs only) | Fully observable |
| Conscious override | No | Yes, when attending |
| Learning direction | Manual reflex creation | Bidirectional: exec→reflex, reflex→exec |
| Reflex improvement | Manual | Reflective, based on outcomes |
| Baseball analogy | Can't practice throwing | Can practice, then automate |

### Open Questions

1. **When should conscious override engage?**
   - User requests deliberate handling?
   - Novel input that partially matches reflex?
   - Low confidence from classifier?

2. **How does knowledge compilation work?**
   - Detect patterns in Claude's responses?
   - Claude proposes reflexes explicitly?
   - Automatic extraction of if-then rules?

3. **How to evaluate reflex quality?**
   - User feedback (thumbs up/down)?
   - Outcome tracking (did user follow up with correction)?
   - Periodic review sessions?

### Practical Override Mechanisms

**The Problem**: If executive must inspect every input to decide whether to override reflex, we've already paid the processing cost - defeating the purpose.

**Source**: [Dual Mechanisms of Control Framework](https://pmc.ncbi.nlm.nih.gov/articles/PMC3289517/)

**Two types of cognitive control**:
- **Proactive**: Set up *before* the event - "I'm going to attend carefully to X"
- **Reactive**: Triggered *by* the event - "This specific input needs attention"

**Practical Options for Bud**:

| Option | Per-input cost | Override capability | Learning |
|--------|---------------|---------------------|----------|
| Proactive mode | ~0 | Yes, when mode set | No |
| Confidence routing | Same as reflex | On low confidence | No |
| Draft-then-review | Small latency | Yes, always | No |
| Async observation | 0 | No (post-hoc) | Yes |

**Option 1: Proactive Mode Setting (Cheap)**
```
Executive sets attention mode in advance:
- "debug_gtd_reflex" → all GTD queries bypass reflex
- "careful_conversation" → bypass all reflexes for thread
- "practice_calendar" → calendar queries go to executive
Cost: Just a flag check
```

**Option 2: Confidence-Based Routing**
```
Reflex outputs confidence score
Below threshold → escalate to executive
Cost: Same as current reflex, with escape hatch
```

**Option 3: Draft-then-Review**
```
Reflex prepares response but doesn't send
Brief window for executive veto
Cost: Small latency
```

**Option 4: Async Observation (LIDA-style)**
```
Reflex fires immediately
Executive sees logs asynchronously
Learning happens offline
Cost: None per-input
```

**Recommended: Proactive + Async**
1. **Default**: Reflexes fire, logs captured
2. **Explicit mode**: "I want to handle X myself" → bypasses reflexes for X
3. **Learning**: Executive periodically reviews logs, improves reflexes

Baseball analogy: Don't consciously review every pitch. Practice with intention (proactive), let muscle memory take over, review video later (async).

---

## Ideas to Explore Further

See `state/ideas.json` for tracked ideas including:
- Zettelkasten-style atomic notes with bidirectional linking
- Temporal knowledge graphs (Zep/Graphiti)
- HippoRAG's PersonalizedPageRank
- Three-tier memory architecture
- Conversation buffer implementation
- Entropy-aware filtering
- Recursive consolidation with affinity clustering
- Dialogue act classification for backchannels
- Focus-based single-stream model
- Global Workspace Theory for attention
- Ambient agent principles
- Talker-Reasoner dual-process architecture
- RouteLLM / cascade routing for cost optimization
- Metacognitive monitoring of reflex layer
- Veto/override mechanism for conscious control
- Knowledge compilation: conscious → automatic

---

## 16. Gap Research: Deep Dive (2026-01-13)

Following the proposed architecture draft, proactive research on identified gaps.

### 16.1 Conversation Buffer Implementation Details

**Research Sources**: [Mem0 Chat History Summarization Guide](https://mem0.ai/blog/llm-chat-history-summarization-guide-2025), [Pinecone LangChain Conversational Memory](https://www.pinecone.io/learn/series/langchain/langchain-conversational-memory/), [Context Rot Research](https://research.trychroma.com/context-rot)

#### Token-Based vs Time-Based Retention

**Hybrid approach recommended** (LangChain ConversationSummaryBufferMemory):
- Monitor token count of raw message buffer
- When limit exceeded, oldest messages → summarized
- Summary replaces raw in context

**Retention strategies**:
1. **Threshold-based**: Compress when tokens exceed limit (e.g., 4000 tokens raw)
2. **Time-based**: Periodically compress older segments (e.g., beyond 10 min)
3. **Importance-weighted**: Score messages by relevance, decay old scores

**Key insight**: "Selective retention over universal compression" - identify what truly matters for future interactions.

#### Summarization Triggers

Best practices from Mem0:
- **Decay mechanisms**: Gradually reduce influence of older memories unless consistently useful
- **Conflict resolution**: Focus on more recent or reliable sources
- **Importance scoring**: Weight conversation elements by future relevance likelihood

#### Multi-Channel Handling

For Bud's use case (Discord channels + autonomous work):
- **Per-channel buffers**: Each Discord channel gets own buffer
- **Focus buffer**: Autonomous work gets separate buffer
- **Cross-channel linking**: When topics span channels, create explicit links

#### Performance Considerations

- Advanced memory systems can reduce token usage by **80-90%** while maintaining quality
- Sub-50ms retrieval times achievable with proper indexing
- Linear relationship: ~0.2ms TTFT increase per input token

#### Recommended Implementation

```
Buffer Structure:
├── raw_buffer: []Message (last 5-10 min or ~3000 tokens)
├── summary_buffer: string (compressed older context)
├── reply_chain: map[string][]string (messageID → replies)
└── scope: "channel:123" | "focus:task-uuid"

Summarization trigger:
  IF raw_buffer.tokens > 3000 OR oldest_message.age > 10min:
    new_summary = LLM.summarize(oldest_half(raw_buffer))
    summary_buffer = merge(summary_buffer, new_summary)
    raw_buffer = newest_half(raw_buffer)
```

### 16.2 Entity Extraction Approaches

**Research Sources**: [spacy-llm GitHub](https://github.com/explosion/spacy-llm), [NER Comparison Study](https://sunscrapers.com/blog/named-entity-recognition-comparison-spacy-chatgpt-bard-llama2/), [spaCy LLM Integration](https://spacy.io/usage/large-language-models)

#### spaCy vs LLM-Based NER

| Approach | Accuracy | Speed | Cost | Best For |
|----------|----------|-------|------|----------|
| spaCy (traditional) | Good | Very fast | Free | Production, real-time |
| LLM (GPT-4, etc.) | Excellent | Slow | $$$ | Complex/nuanced context |
| Small LLM (local) | Good | Fast | Local | Balance of both |
| spacy-llm hybrid | Best of both | Medium | Variable | Prototyping → Production |

#### Key Findings

- **LLMs understand whole-document context** → more accurate for ambiguous entities
- **Significant gap** between spaCy and modern LLMs in accuracy
- **Custom trained small models** often sufficient for domain-specific NER
- **spacy-llm** allows mixing: LLM for prototyping, trained models for production

#### Recommended Approach for Bud

**Two-tier entity extraction**:
1. **Fast path (spaCy/small model)**: Standard entities (people, dates, locations)
   - Use spaCy's `en_core_web_sm` or train domain-specific model
   - Run on every message for tagging

2. **Deep path (Ollama LLM)**: Complex/ambiguous entities
   - Run during consolidation or when fast path uncertain
   - Extract: project names, preferences, relationships
   - Use structured extraction prompt

#### Entity Resolution

From Zep/Graphiti approach:
- Match extracted entities against existing graph nodes
- Use embedding similarity for fuzzy matching
- Merge when confidence > 0.85, create new otherwise
- Track aliases for the same entity

### 16.3 Graph Database Implementation

**Research Sources**: [Synapse Paper](https://arxiv.org/html/2601.02744v1), [SQLite Graph Extension](https://github.com/agentflare-ai/sqlite-graph), [Cayley Graph DB](https://github.com/topics/graph-database?l=go), [Cozo DB](https://lobste.rs/s/gcepzn/cozo_new_graph_db_with_datalog_embedded)

#### Embedded Options for Go

| Database | Language | Query Language | Notes |
|----------|----------|----------------|-------|
| Cayley | Go | GraphQL/Gizmo | 14k stars, Google-inspired, mature |
| sqlite-graph | C/SQLite | SQL + extensions | Lightweight, embedded |
| Cozo | Rust | Datalog | SQLite-like simplicity, powerful |
| Custom | Go | Custom | Full control, tailored to needs |

#### Synapse's Spreading Activation Algorithm (CRITICAL)

**From Synapse paper (2026)** - State of the art for LLM agent memory:

**Graph Structure**:
- **Episodic layer**: (content, embedding, timestamp) nodes with temporal edges
- **Semantic layer**: Extracted concepts with bidirectional links to episodes
- **Sparse enforcement**: Max K=15 incoming edges per node
- **Active graph limit**: ~10,000 nodes (dormant archived to disk)

**Spreading Activation Formula**:
```
u_i(t+1) = (1-δ) * a_i(t) + Σ_{j∈N(i)} S * w_ji * a_j(t) / fan(j)

Where:
- δ = decay factor
- S = spreading coefficient
- w_ji = edge weight (temporal decay or semantic similarity)
- fan(j) = degree_out(j) (dilutes attention based on node degree)
```

**Three-step cycle** (T=3 iterations to stability):
1. **Propagation**: Energy spreads to neighbors
2. **Lateral inhibition**: High-activation nodes suppress competitors
3. **Sigmoid transformation**: Convert to firing rates

**Retrieval initiation** (dual triggers):
- BM25 for named entities (lexical match)
- Dense embeddings for conceptual similarity (semantic match)

#### "Feeling of Knowing" Rejection Protocol

**Critical for hallucination prevention**:
1. **Confidence gating**: Retrieval confidence = activation energy of top node
   - If confidence < threshold (τ_gate = 0.12): reject query preemptively
   - Don't generate potentially hallucinated response

2. **Explicit verification**: For borderline cases
   - Prompt: "Is this EXPLICITLY mentioned? If not, output 'Not mentioned.'"
   - Forces distinction between parametric knowledge and grounded retrieval

**Results**: 96.6 F1 on adversarial rejection, false refusal rate < 2.5%

#### Performance Optimizations

From Synapse:
- **HNSW indexing** for pairwise similarity checks
- **Incremental graph construction** (online, as agent interacts)
- **Archival strategy**: Dormant nodes to disk, active in memory
- **95% token reduction** vs full-context, 23% accuracy improvement on multi-hop

#### Recommended Implementation

Given Go codebase and simplicity needs:

**Option 1: SQLite + Custom Graph Layer** (recommended)
- Store nodes in SQLite tables (episodes, entities, traces)
- Store edges in adjacency table with weights
- Implement spreading activation in Go
- Use existing Ollama embeddings for vectors
- Pros: Portable, queryable, debuggable

**Option 2: Cayley**
- Mature Go library, good for complex traversals
- Might be overkill for our needs
- Pros: Built-in graph algorithms

### 16.4 Knowledge Compilation Mechanism

**Research Sources**: [ACT-R Proceduralization](http://cognitivemodelinglab.com/index.php/act-r-code-metaknowledge-proceduralization/), [PMSA Paper](https://arxiv.org/html/2511.22074v2), [ACT-R Learning Theory](https://www.lrdc.pitt.edu/schunn/research/papers/nomagicbullets.pdf)

#### ACT-R's Proceduralization Model

**Core mechanism**: "Reward what is faster"
- Productions move from slow/knowledge-driven (macro-cognition) to fast/automatic (micro-cognition)
- Compilation creates shorter chains to replace longer ones

**Analogy Learning**:
1. Encounter situation requiring goal-solving
2. Find declarative knowledge that solves it
3. Create production rule from the pairing
4. Reinforce through repetition

**Key insight**: Learning is not from examples alone, but from *successfully applied* examples.

#### Procedural Memory Synthesis (PMSA) Approaches

Modern AI agent approaches (from research):

1. **PRAXIS**: State-dependent memory retrieval
   - Stores: (pre-state, internal state, action, post-state)
   - Retrieval via IoU + embedding similarity
   - Not true compilation, but pattern matching

2. **CangLing-KnowFlow**: Procedural Knowledge Base
   - 1,008 expert-validated workflow templates as DAGs
   - Pre-structured rather than learned

3. **HERAKLES**: Hierarchical Skill Compilation
   - Decomposes complex tasks into reusable skills
   - Skills stored and retrieved for similar future tasks

#### Pattern Detection for Reflex Proposal

For Bud's knowledge compilation (executive → reflex):

**Detection Algorithm**:
```
During idle/metacognitive periods:
1. Group executive response logs by:
   - Input source/type (discord greeting, GTD query, etc.)
   - Response pattern (action type, structure)

2. Identify candidates where:
   - Same input pattern occurs 3+ times
   - Same response pattern each time
   - Response was successful (no correction/retry)
   - Confidence high (no ambiguity noted)

3. Extract pattern:
   - Input: regex or intent classification
   - Output: template with variable slots

4. Generate reflex proposal:
   - YAML with trigger pattern
   - Pipeline with templated response
   - Include example derivations
```

**Confidence Thresholds**:
- Minimum repetitions: 3
- Success rate: 100% (all applications worked)
- Response similarity: > 0.9 cosine similarity between responses
- No manual overrides in history

**Approval Workflow**:
1. Propose to user: "I notice I always respond to X with Y. Create reflex?"
2. Show examples: "Here are the 3 times this happened: ..."
3. User approves/rejects/modifies
4. If approved, generate YAML and add to reflexes

### 16.5 Evaluation Metrics

**Research Sources**: [LoCoMo Benchmark](https://snap-research.github.io/locomo/), [MemoryAgentBench](https://arxiv.org/html/2507.05257v1), [A-MEM Paper](https://arxiv.org/pdf/2502.12110)

#### LoCoMo Framework (Long-term Conversational Memory)

**Three evaluation tasks**:

1. **Question Answering** (F1 score)
   - Single-hop reasoning
   - Multi-hop reasoning
   - Temporal reasoning (models lag humans by 73%)
   - Commonsense/world knowledge
   - Adversarial questions

2. **Event Graph Summarization**
   - Measures causal/temporal understanding
   - Can model extract event structure?

3. **Multimodal Dialog Generation**
   - Uses recalled context for coherent responses
   - Tests narrative consistency

**Benchmark insights**:
- Models underperform humans by 56% overall
- RAG improves QA by 22-66%
- 300 turns, 9K tokens per conversation

#### MemoryAgentBench Framework (2025)

**Four evaluation dimensions** (all critical for Bud):

| Dimension | Description | Current SOTA |
|-----------|-------------|--------------|
| **AR** (Accurate Retrieval) | Extract correct snippet for query | RAG agents good |
| **TTL** (Test-Time Learning) | Learn new behaviors during deployment | Long-context models best |
| **LRU** (Long-Range Understanding) | Integrate info across 100k+ tokens | Long-context models best |
| **CR** (Conflict Resolution) | Update outdated information | CRITICAL GAP: 60% single-hop, <7% multi-hop |

**Key finding**: Conflict Resolution is the hardest problem - all methods fail at multi-hop conflicts.

#### Recommended Metrics for Bud

**1. Conversation Continuity** (addresses original problem)
```
Metric: Reply-Context Accuracy
- Ground truth: Manual annotation of "what is this replying to?"
- Predicted: System's inferred context
- Score: % of replies correctly linked to their context

Test cases:
- "yes" following a question → should retrieve question
- "sounds good" after proposal → should retrieve proposal
- Delayed responses → should still find relevant prior
```

**2. Memory Retrieval Quality**
```
Metric: Retrieval F1 @ k
- For given query, what memories should be retrieved?
- Manual annotation of relevant memories
- Measure precision/recall at k=5, k=10

Additional: Multi-hop Retrieval Accuracy
- For queries requiring multiple memory hops
- Currently hardest for all systems
```

**3. Entropy Filter Effectiveness**
```
Metric: Low-Info Rejection Rate
- Track what gets filtered vs stored
- Manual review of false positives (useful stuff filtered)
- Manual review of false negatives (noise stored)
- Target: <5% false positive, <10% false negative
```

**4. Reflex Effectiveness**
```
Metric: Reflex Success Rate
- % of reflex-handled inputs that didn't require correction
- Track escalations to executive
- Track user corrections/overrides

Metric: Knowledge Compilation Rate
- How many executive patterns became reflexes?
- Time from pattern detection to reflex creation
```

**5. User Satisfaction Signals**
```
Implicit signals:
- Response corrections ("no, I meant...")
- Repeated questions (memory failed)
- Explicit frustration markers

Explicit signals:
- Direct feedback ("good memory!" / "you forgot")
- Task completion rate
```

---

## Summary: Research Complete

All five gap areas from proposed-architecture.md have been researched:

1. **Conversation Buffer**: Hybrid token/time-based, with importance weighting and summarization triggers. Per-channel scope recommended.

2. **Entity Extraction**: Two-tier approach (spaCy fast path + Ollama deep path), with entity resolution against existing graph.

3. **Graph Implementation**: SQLite + custom graph layer recommended. Synapse's spreading activation algorithm provides state-of-the-art retrieval with "feeling of knowing" rejection.

4. **Knowledge Compilation**: Pattern detection over executive logs, 3+ repetitions with 100% success, user approval workflow.

5. **Evaluation Metrics**: LoCoMo + MemoryAgentBench frameworks. Focus on Reply-Context Accuracy for continuity, Retrieval F1 for quality, Conflict Resolution as hardest problem.

**Key new discoveries**:
- Synapse paper (2026) provides complete spreading activation implementation
- MemoryAgentBench identifies Conflict Resolution as critical gap (7% accuracy)
- "Feeling of knowing" protocol for hallucination rejection
- spacy-llm hybrid approach for flexible NER pipeline
