# Reflex System

Reflexes are automated responses that run without waking the executive (Claude).

## Philosophy

- Pluggable pipelines defined in YAML
- I can write my own reflexes
- Composable actions chained together
- Located in state/system/reflexes/

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

## Classifier Types

### Regex Classifier (default)

Fast pattern matching for specific phrases:

```yaml
name: quick-add
trigger:
  source: discord
  classifier: regex  # or omit - regex is default
  pattern: "^add (.+) to inbox$"
  extract: [item]
pipeline:
  - action: gtd_add
    title: "{{.item}}"
  - action: reply
    message: "Added '{{.item}}' to inbox"
```

### Ollama Classifier

Uses local LLM for natural language understanding:

```yaml
name: gtd-handler
trigger:
  source: discord
  classifier: ollama
  model: qwen2.5:7b  # optional, this is default
  intents:
    - gtd_show_today
    - gtd_show_inbox
    - gtd_add_inbox
    - not_gtd
  # prompt: "Custom classification prompt..."  # optional
pipeline:
  - action: gate
    condition: "{{.intent}} == not_gtd"
    stop: true
  - action: gtd_dispatch
    output: response
  - action: reply
    message: "{{.response}}"
```

The `{{.intent}}` variable is populated with the classified intent.

### None Classifier

Always matches if source/type filters pass:

```yaml
name: catch-all
trigger:
  source: discord
  classifier: none
pipeline:
  # ...
```

## Core Actions (Built-in)

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
| `add_bud_task` | Add to tasks.json |
| `add_idea` | Add to ideas.json |
| `log` | Write to journal |
| `gate` | Conditionally stop pipeline |
| `gtd_list` | List GTD tasks |
| `gtd_add` | Add task to inbox |
| `gtd_complete` | Complete a task |
| `gtd_dispatch` | Route by intent |

## GTD Actions

| Action | Description | Parameters |
|--------|-------------|------------|
| `gtd_list` | List tasks | `when` (inbox, today, anytime, someday) |
| `gtd_add` | Add task to inbox | `title`, `notes` (optional) |
| `gtd_complete` | Complete a task | `id` or `title` (fuzzy match) |
| `gtd_dispatch` | Route by intent | uses `{{.intent}}` from classifier |

## Gate Action

Conditionally stop pipeline execution:

```yaml
- action: gate
  condition: "{{.intent}} == not_gtd"
  stop: true
```

## Creating Reflexes

To create a new reflex:
1. Write YAML definition following the format above
2. Save to state/system/reflexes/
3. Reflexes are loaded on next restart

## MCP Tools

Use these tools to manage reflexes:

- `create_reflex` - Create a new reflex with name, pattern, and pipeline
- `list_reflexes` - See all defined reflexes
- `delete_reflex` - Remove a reflex by name

Example:
```json
{
  "name": "greet-back",
  "description": "Respond to greetings",
  "pattern": "^(hi|hello|hey)\\b",
  "pipeline": [
    {"action": "template", "output": "msg", "template": "Hello! How can I help?"},
    {"action": "reply", "message": "{{.msg}}"}
  ]
}
```
