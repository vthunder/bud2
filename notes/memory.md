# Memory Architecture

## Core Insight: Activation Rules Everything

Memory isn't "loaded" - it's **activated**. What ends up in context (working memory) is determined by what signals activate.

This unifies the memory model:
- Core memory = always activated
- Tool descriptions = activated based on relevance
- Context = activated memories + ephemeral data

## Memory Layers

| Layer | What It Is | Activation |
|-------|-----------|------------|
| **Long-term** | Everything stored | Source of all activations |
| **Working** | Currently activated memories + ephemeral | What's in context right now |
| **Core** | Subset of long-term | Always activated (identity, basic purpose) |

```
Long-term Memory (persistent storage)
┌─────────────────────────────────────────────────────────┐
│                                                         │
│  Core memories (always activated):                      │
│    - Identity: "I am Bud..."                           │
│    - Basic purpose, values                              │
│    - Core tools: memory recall, respond to user         │
│                                                         │
│  Context-activated memories:                            │
│    - Project contexts                                   │
│    - User preferences                                   │
│    - Conversation history                               │
│    - Specialized tools (email, calendar, etc.)         │
│                                                         │
└─────────────────────────────────────────────────────────┘
              │
              │ activation (signal-driven)
              ▼
Working Memory (what's in context)
┌─────────────────────────────────────────────────────────┐
│                                                         │
│  Always present:                                        │
│    - Core identity                                      │
│    - Core tools                                         │
│                                                         │
│  Activated by current signal:                           │
│    - Relevant project context                           │
│    - Relevant conversation history                      │
│    - Relevant specialized tools                         │
│                                                         │
│  Ephemeral (not in long-term):                         │
│    - Current percepts                                   │
│    - Thread goal/state                                  │
│    - Immediate conversation                             │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

## Activation Mechanism

When a signal arrives, we need to find relevant memories:

```
Signal arrives
    │
    ▼
Extract activation keys:
  - Explicit: tags, keywords, entity names
  - Implicit: semantic similarity (embeddings)
    │
    ▼
Query long-term memory:
  - Always include: core memories
  - Match by: tags, keywords, semantic similarity
    │
    ▼
Rank and select:
  - Relevance score
  - Recency (recent memories more likely)
  - Context budget (can't load everything)
    │
    ▼
Working memory = core + activated + ephemeral
```

## Tools as Memory

Tool descriptions are stored in long-term memory, activated like any other memory:

| Tool Category | Activation |
|--------------|------------|
| **Core tools** | Always activated |
| **Domain tools** | Activated by signal relevance |

**Core tools (always loaded):**
- `recall_memory` - search/load more memory
- `respond_to_user` - send message via effector
- `update_thread` - modify thread state
- `complete_thread` - mark thread done

**Domain tools (activated by context):**
- `send_email` - activated when email-related signal
- `create_github_issue` - activated when GitHub-related
- `schedule_meeting` - activated when calendar-related

This means:
- New tools can be added to long-term memory
- They automatically become available when relevant
- Context stays lean (no loading unused tool descriptions)

## Memory Storage

Long-term memory needs:
- Content (the actual memory)
- Metadata for activation:
  - Tags/keywords (explicit)
  - Embedding vector (semantic)
  - Category (core, tool, context, etc.)
  - Last accessed (for recency)

```json
{
  "id": "mem-001",
  "type": "identity",
  "category": "core",
  "tags": ["identity", "purpose"],
  "content": "I am Bud, a personal AI assistant...",
  "embedding": [0.1, 0.2, ...],
  "created_at": "...",
  "accessed_at": "..."
}
```

## Comparison to Bud1

| Bud1 | Bud2 |
|------|------|
| Layers pre-loaded at startup | Activation determines what's loaded |
| All core/working memory in every prompt | Only activated memories in context |
| Tools defined in code | Tools stored as memory, activated |
| Fixed context structure | Dynamic context based on signal |

## Open Questions

1. **Activation implementation** - Embeddings? Tags? Both?
2. **Context budget** - How much can we load? How to prioritize?
3. **Memory persistence** - SQLite? JSON files? Vector DB?
4. **Forgetting** - How do old memories decay/consolidate?
5. **Learning** - How do new memories get created and tagged?
