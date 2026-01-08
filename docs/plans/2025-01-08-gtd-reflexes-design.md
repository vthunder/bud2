# GTD Reflexes Design

## Overview

Add reflex-based handling for simple GTD operations, bypassing the executive for fast responses while maintaining memory continuity for follow-up questions.

## Core Model

```
Discord Message (raw input)
         ↓
    ┌────────────────────────────────────┐
    │ Reflex Layer                        │
    │                                     │
    │ 1. Check all reflexes for matches   │
    │ 2. First/highest-priority wins      │
    │ 3. Run pipeline, collect output     │
    └────────────────────────────────────┘
         ↓
    Create Percept:
      • Reflex output (raw input embedded as reference)
      • OR raw input if no reflex matched
         ↓
    ┌────────────────────────────────────┐
    │ Attention                           │
    │                                     │
    │ Routes based on:                    │
    │ - Intensity/salience                │
    │ - Whether reflex handled it         │
    │ - Needs executive judgment?         │
    └────────────────────────────────────┘
         ↓
    Executive (if needed)
         ↓
    Response
```

**Key principles:**
- Reflexes process raw input, not percepts (no looping by design)
- One reflex per input (first match or priority-based)
- Chaining = longer pipelines, not reflex → reflex
- Reflex output subsumes raw input (raw preserved as reference)

## Trigger Types

### Regex Classifier (fast, specific patterns)

```yaml
name: quick-add
trigger:
  source: discord
  classifier: regex
  pattern: "^add (.+) to inbox$"
  extract: [item]
pipeline:
  - action: gtd_add
    title: "{{.item}}"
  - action: reply
    message: "Added '{{.item}}' to inbox"
```

### Ollama Classifier (flexible, natural language)

```yaml
name: gtd-handler
trigger:
  source: discord
  classifier: ollama
  model: qwen2.5:7b
  intents:
    - gtd_show_today
    - gtd_show_inbox
    - gtd_add_inbox
    - gtd_complete
    - not_gtd
pipeline:
  - action: gate
    condition: "{{.intent}} == not_gtd"
    stop: true
  - action: gtd_dispatch
    output: result
  - action: reply
    message: "{{.result}}"
```

## Updated Trigger Struct

```go
type Trigger struct {
    // Existing fields
    Pattern string   `yaml:"pattern"` // regex pattern (for classifier: regex)
    Extract []string `yaml:"extract"` // named capture groups
    Source  string   `yaml:"source"`  // filter by source (discord, github, etc)
    Type    string   `yaml:"type"`    // filter by type (message, etc)

    // New fields for Ollama classification
    Classifier string   `yaml:"classifier"` // "regex" (default), "ollama", or "none"
    Model      string   `yaml:"model"`      // Ollama model (default: qwen2.5:7b)
    Intents    []string `yaml:"intents"`    // valid intents for classification
    Prompt     string   `yaml:"prompt"`     // optional custom classification prompt
}
```

## Immediate Trace Creation

When a reflex handles a query, traces are created immediately (bypassing consolidation delay) so follow-up questions have context.

```go
// CreateImmediateTrace creates a trace available immediately
func (a *Attention) CreateImmediateTrace(content string, source string) *types.Trace {
    trace := &types.Trace{
        ID:        generateID(),
        Content:   content,
        Source:    source,
        Timestamp: time.Now(),
        Embedding: a.embed(content),
    }
    a.traces.Add(trace)
    trace.Activation = 0.8  // High activation for immediate relevance
    return trace
}
```

**Usage in reflex engine:**
```go
// After reflex fires successfully
queryTrace := attention.CreateImmediateTrace(
    fmt.Sprintf("User asked: %s", rawInput),
    "reflex-query",
)
responseTrace := attention.CreateImmediateTrace(
    fmt.Sprintf("Reflex responded: %s", result),
    "reflex-response",
)
```

**Follow-up scenario:**
```
User: "show today's tasks"
Bud: "1. Buy milk  2. Call dentist"
  → Creates traces for query + response

User: "move the first one to tomorrow"
  → Spreading activation finds "Showed 3 tasks: Buy milk..."
  → Executive sees in "Relevant Memories"
  → Knows "first one" = Buy milk
```

## GTD Actions

New actions for the reflex action registry:

| Action | Purpose | Parameters |
|--------|---------|------------|
| `gtd_list` | List tasks | `when`, `project`, `area` (optional) |
| `gtd_add` | Quick add to inbox | `title`, `notes` (optional) |
| `gtd_complete` | Mark task done | `id` or `title` (fuzzy match) |
| `gtd_dispatch` | Route by intent | `intent` (from classifier) |
| `gate` | Conditional stop | `condition`, `stop` |

## Updated Percept Type

```go
type Percept struct {
    // ... existing fields ...

    // Raw input preserved for context/debugging
    RawInput string `json:"raw_input,omitempty"`

    // What reflex processed this (empty if none)
    ProcessedBy []string `json:"processed_by,omitempty"`
}
```

## Reflex vs Executive Split

| Reflexive (fast, simple) | Executive (needs thinking) |
|--------------------------|---------------------------|
| "show today's tasks" | "help me prioritize today" |
| "what's in my inbox" | "should I move X to today?" |
| "add X to inbox" | "break down project X into tasks" |
| "mark X as done" | "what should I work on next?" |
| "show my projects" | "review my someday list" |

## Files to Create/Modify

### New Files
- None (extend existing)

### Modified Files
- `cmd/bud/main.go` - Hook reflex engine before attention
- `internal/reflex/types.go` - Add Classifier, Model, Intents to Trigger
- `internal/reflex/engine.go` - Add Ollama classification in Match()
- `internal/reflex/actions.go` - Add gtd_*, gate actions
- `internal/attention/attention.go` - Add CreateImmediateTrace()
- `internal/types/percept.go` - Add RawInput, ProcessedBy fields
- `state/notes/reflexes.md` - Document Ollama classifier

### New Reflex Files
- `state/reflexes/gtd-handler.yaml` - Main GTD reflex with Ollama

## Test Scenarios

1. **Simple reflex works**
   - "show today's tasks" → reflex fires, returns list, no executive

2. **Follow-up has context**
   - "show today's tasks" → "move the first one" → executive has trace context

3. **Ambiguous escalates**
   - "what should I work on?" → classified as not_gtd → goes to executive

4. **Reflex failure fallback**
   - Ollama down → reflex fails → raw input goes to attention normally

5. **Priority ordering**
   - Multiple reflexes match → highest priority (or first) wins

## Implementation Order

1. Add Classifier fields to Trigger type
2. Add Ollama classification to reflex engine Match()
3. Add gate action
4. Add GTD actions (gtd_list, gtd_add, gtd_complete, gtd_dispatch)
5. Add CreateImmediateTrace() to attention
6. Add RawInput/ProcessedBy to Percept
7. Hook reflex engine into cmd/bud/main.go
8. Create gtd-handler.yaml reflex
9. Update reflexes.md documentation
10. Write and run test scenarios
