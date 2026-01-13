# Autonomous Behavior Design

## Goals

1. Bud works on its own, following its own train of thought
2. Observability so owner can see what Bud is doing
3. Runs often when there's interesting work
4. Uses lower levels (reflexes, local LLM) before expensive executive (Claude)
5. User interaction takes priority over autonomous work

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         EXECUTIVE (Claude)                       │
│  Only invoked when lower layers can't handle                     │
│  Expensive, slow, powerful                                       │
├─────────────────────────────────────────────────────────────────┤
│                         ATTENTION                                │
│  Scores all inputs (percepts + impulses)                        │
│  Routes to executive or reflexes                                │
│  User threads get highest salience                              │
├─────────────────────────────────────────────────────────────────┤
│                         REFLEXES                                 │
│  Pluggable pipelines (YAML-defined)                             │
│  Level 0: Pattern match (instant)                               │
│  Level 1: Heuristics (fast)                                     │
│  Level 2: Local LLM - Ollama (cheap)                            │
├─────────────────────────────────────────────────────────────────┤
│                    SENSES + IMPULSES                             │
│  Senses: Discord, GitHub, Calendar (external)                   │
│  Impulses: Tasks, Ideas, Schedule (internal)                    │
└─────────────────────────────────────────────────────────────────┘
```

## Inputs to Attention

### Percepts (External - Sensed)
- Discord messages (real-time via websocket)
- GitHub webhooks
- Calendar events
- File changes

### Impulses (Internal - Motivated)
- Task due (from tasks.json)
- Idea to explore (from ideas.json)
- Scheduled item (from schedule.json)
- Commitment follow-up

Percepts and impulses are scored together by attention. User messages naturally
get high salience, so autonomous work yields to user interaction.

## Data Structures

### Shared Files (Claude + System)

```
state/
├── tasks.json           # Commitments: "I will do X"
├── ideas.json           # Curiosities: "I want to explore X someday"
├── schedule.json        # Recurring: "Check Y every morning"
├── journal.jsonl        # Activity log for observability
└── reflexes/            # Pluggable reflex definitions (YAML)
```

### tasks.json
```json
[
  {
    "id": "task-abc123",
    "task": "Review PR #42",
    "due": "2026-01-07T10:00:00Z",
    "priority": 1,
    "context": "Promised in conversation"
  }
]
```

### ideas.json
```json
[
  {
    "id": "idea-xyz789",
    "idea": "Research how biological memory consolidation works",
    "sparked_by": "conversation about memory architecture",
    "added": "2026-01-06T15:00:00Z"
  }
]
```

### schedule.json
```json
[
  {
    "name": "morning-review",
    "cron": "0 9 * * *",
    "action": "Check inbox, review commitments, plan day"
  }
]
```

### journal.jsonl (Observability)
```jsonl
{"ts":"...","type":"decision","context":"user asked X","reasoning":"...","action":"..."}
{"ts":"...","type":"impulse","source":"idea","idea":"explore Y","decision":"deferred"}
{"ts":"...","type":"reflex","pattern":"greeting","action":"wave","skipped_executive":true}
{"ts":"...","type":"exploration","idea":"research Z","duration_sec":120,"outcome":"learned A,B,C"}
```

Purpose: Let Bud answer "what did you do today?" and "why did you do that?"

## Reflex System

### Philosophy
- MCP-inspired pluggable pipelines
- Bud can write its own reflexes
- Composable actions chained together
- YAML definitions in state/reflexes/

### Reflex Definition Format
```yaml
name: summarize-url
description: Fetch a URL and return a summary
trigger:
  pattern: "summarize (https?://\\S+)"
  extract: [url]
pipeline:
  - action: fetch_url
    input: $url
    output: content
  - action: ollama_prompt
    model: qwen2.5:14b
    prompt: "Summarize in 2-3 sentences: {{content}}"
    output: summary
  - action: reply
    message: "{{summary}}"
```

### Core Actions (Built-in)
| Action | Description |
|--------|-------------|
| `fetch_url` | HTTP GET, return content |
| `read_file` | Read local file |
| `write_file` | Write local file |
| `ollama_prompt` | Run prompt through local LLM |
| `extract_json` | JSONPath extraction |
| `github_api` | GitHub API call |
| `reply` | Send Discord message |
| `react` | Add emoji reaction |
| `add_task` | Add to tasks.json |
| `add_idea` | Add to ideas.json |
| `log` | Write to journal |

### Reflex Levels
- **Level 0**: Pattern match only (instant, no LLM)
- **Level 1**: Heuristics + simple processing (fast)
- **Level 2**: Ollama for triage/processing (cheap, ~1-2s)
- **Level 3**: Escalate to executive (expensive)

## Core Memory Approach

Keep core_seed.md lean with pointers to detailed docs:

```markdown
# core_seed.md (brief)

I maintain my own task queue, ideas backlog, and schedule in state/.
See state/notes/systems.md for formats and usage.

I can define and extend reflexes - automated responses that run without
waking me. See state/notes/reflexes.md for the reflex system.

My activity is logged to state/journal.jsonl. I use this to answer
questions about what I've been doing and why.
```

Detailed documentation lives in state/notes/:
- systems.md - Task queue, ideas, schedule formats
- reflexes.md - How to create and use reflexes
- actions.md - Available reflex actions

## Autonomous Loop

```
Every N seconds (adaptive: 5s active, 60s idle):
  1. Check impulse sources:
     - tasks.json: anything due?
     - schedule.json: anything triggered?
     - ideas.json: anything interesting? (only if truly idle)

  2. For each impulse:
     - Score salience
     - Add to attention pool

  3. Attention selects highest salience input
     - User percept? → Executive (with typing indicator)
     - Reflex can handle? → Run pipeline
     - Needs executive? → Check budget → Process
```

## Typing Indicator

When executive is processing a thread with a Discord channel:
1. Start typing indicator when processing begins
2. Maintain it while Claude is thinking
3. Stop when response is sent or processing ends

## Budget Integration

Before autonomous executive work:
- Check ThinkingBudget.CanDoAutonomousWork()
- Daily limit, active session check, minimum interval
- CPU watcher as fallback for session completion

## User Priority

No special "user activity detection" needed:
- User messages create high-salience percepts
- Attention naturally selects them first
- Autonomous impulses wait their turn
- If mid-thought when user messages, finish then switch

## Implementation Order

1. **Journal logging** - Observability foundation
2. **Typing indicator** - UX improvement
3. **Lean core memory + notes/** - Documentation structure
4. **ideas.json + tasks.json** - Internal motivation sources
5. **Impulse abstraction** - Separate from percepts
6. **Reflex YAML framework** - Pipeline definitions
7. **Core actions** - fetch, ollama, reply, etc.
8. **Claude can create reflexes** - Self-extending system

## Open Questions (for later)

- How does Bud decide which idea to explore?
- How do reflexes get promoted/demoted based on usefulness?
- Should reflexes have analytics (how often fired, success rate)?
- How to handle reflex errors gracefully?
