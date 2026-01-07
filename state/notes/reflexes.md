# Reflex System

Reflexes are automated responses that run without waking the executive (Claude).

## Philosophy

- Pluggable pipelines defined in YAML
- I can write my own reflexes
- Composable actions chained together
- Located in state/reflexes/

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

## Creating Reflexes

To create a new reflex:
1. Write YAML definition following the format above
2. Save to state/reflexes/
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
