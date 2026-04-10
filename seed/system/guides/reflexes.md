# Reflex System

Reflexes are automated responses that run without waking the executive (Claude).

## Philosophy

- Pluggable pipelines defined in YAML
- Composable actions chained together via a shared context bag (`map[string]any`)
- Located in `state/system/reflexes/`
- Reflexes are a clean API layer — not a scripting language. For conditional logic and loops, use `shell` or spawn a subagent.

## Reflex Levels

| Level | Description | Speed | Cost |
|-------|-------------|-------|------|
| 0 | Pattern match only | Instant | Free |
| 1 | Heuristics + simple processing | Fast | Free |
| 2 | Ollama (local LLM) | ~1-2s | Cheap |
| 3 | Escalate to executive | Slower | Expensive |

## Reflex Definition Format

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

### Callable Reflexes

Set `callable: true` to make a reflex only invocable programmatically (not auto-matched against percepts). Used for sub-reflexes in composition:

```yaml
name: gtd-add
callable: true
pipeline:
  - action: call_tool
    tool: gtd_add
    params: {title: "{{.content}}"}
  - action: reply
    message: "Added to inbox."
```

## Classifier Types

### Regex Classifier (default)

Fast pattern matching for specific phrases:

```yaml
trigger:
  source: discord
  classifier: regex  # or omit - regex is default
  pattern: "^add (.+) to inbox$"
  extract: [item]
```

### Ollama Classifier

Uses local LLM for natural language understanding. Sets `{{.intent}}` in the context bag:

```yaml
trigger:
  source: discord
  classifier: ollama
  model: qwen2.5:7b  # optional, this is default
  intents:
    - add
    - query
    - not_gtd
  # prompt: "Custom classification prompt..."  # optional
```

### None Classifier

Always matches if source/type filters pass:

```yaml
trigger:
  source: discord
  classifier: none
```

### Impulse Source

Matches on impulse events instead of message patterns:

```yaml
trigger:
  source: impulse:meeting_reminder
```

## Pipeline Actions

All actions read/write the shared context bag. Each step can reference `{{.varname}}` from prior steps.

### Communication

| Action | Description |
|--------|-------------|
| `reply` | Send Discord message |
| `react` | Add emoji reaction |
| `log` | Write to journal |

### Data & Logic

| Action | Description |
|--------|-------------|
| `call_tool` | Call any MCP tool (things, calendar, etc.) |
| `json_query` | JQ-style transform on context data |
| `template` | Go template rendering into a variable |
| `ollama_prompt` | Run a local LLM prompt |
| `fetch_url` | HTTP GET |
| `shell` | Execute shell command (escape hatch for conditional logic, loops) |
| `gate` | Conditionally **stop** the pipeline (does not route — use sub-reflexes for branching) |

### Composition

| Action | Description |
|--------|-------------|
| `invoke_reflex` | Call another reflex by name. Supports `on_missing: escalate` — if target doesn't exist, escalates to executive. |
| `escalate` | Explicitly escalate to the executive with optional message. Forwards full context (see below). |

## Escalation and Context Forwarding

When a reflex escalates (either via `escalate` action or `invoke_reflex` with `on_missing: escalate`), the executive receives **full context** about what the reflex already did:

| Context field | Content |
|---------------|---------|
| `_reflex_escalated` | `true` |
| `_reflex_name` | Name of the reflex that escalated |
| `_reflex_step` | Index of the step that triggered escalation |
| `_escalate_message` | Custom message from the `escalate` action (if set) |
| `_escalate_vars` | Snapshot of the full context bag at escalation point |

The executive prompt renders a **Reflex Escalation** section, so the exec starts with pre-fetched data rather than the raw original percept. This enables the pattern: **pre-fetch in reflex, reason in exec**.

### Explicit `escalate` action

```yaml
- action: escalate
  params:
    message: "User wants to {{.intent}}: {{.content}}. GTD list: {{.tasks}}"
```

This is preferred over `invoke_reflex` on a non-existent target for intentional escalation.

## Composition Pattern (GTD Dispatcher Example)

```
gtd-dispatcher:
  1. Classify intent with Ollama → .intent
  2. invoke_reflex gtd-{{.intent}}  (routes to callable sub-reflex)
  3. Sub-reflex executes, replies

If intent is ambiguous or sub-reflex missing → escalate to exec with intent in context
```

This is a mini-workflow: **classify → route → execute → reply**, no exec wake unless needed.

## Gate Action

Conditionally stop pipeline execution. Does not route — for routing, use `invoke_reflex` to a sub-reflex per branch:

```yaml
- action: gate
  condition: "{{.intent}} == not_gtd"
  stop: true
```

## Creating Reflexes

To create a new reflex:
1. Write YAML definition following the format above
2. Save to `state/system/reflexes/`
3. Reflexes are loaded on next restart

## MCP Tools

Use these tools to manage reflexes:

- `create_reflex` — Create a new reflex
- `list_reflexes` — See all defined reflexes
- `delete_reflex` — Remove a reflex by name

## Design Guidelines

- **Keep reflexes as an API layer**, not a scripting language. Avoid replicating branch/loop/conditional logic in YAML — use `shell` for scripts or escalate to the exec.
- **Pre-fetch, then escalate** — use pipeline steps to gather data (tool calls, classifier output, memory queries), then escalate with that context so the exec starts informed.
- **Compose via `invoke_reflex`** — build workflows from small callable sub-reflexes rather than large monolithic pipelines.
- **`callable: true`** for sub-reflexes that should only be invoked by other reflexes, not auto-matched.
