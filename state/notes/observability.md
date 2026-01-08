# Observability Guide

This guide helps you answer questions about your own activity and reasoning.

## Activity Log

The activity log (`activity.jsonl`) automatically records all events:

| Event Type | What it captures |
|------------|------------------|
| `input` | Messages received from users |
| `reflex` | Queries handled by reflexes (GTD queries, etc.) |
| `reflex_pass` | Queries that passed through reflexes to executive |
| `executive_wake` | When you (the executive) started processing |
| `executive_done` | When you finished processing (with duration) |
| `action` | Actions you took (sending messages, reactions) |
| `decision` | Explicit decisions logged via journal_log |
| `error` | Errors encountered |

## MCP Tools for Self-Inspection

### activity_today
Returns today's activity. Use for questions like:
- "What did you do today?"
- "How many messages did you handle today?"

### activity_recent
Returns recent activity (default 50 entries). Use for:
- "What happened in the last hour?"
- "Show me recent activity"

### activity_search
Search activity by text. Use for:
- "Did I help Tim with X?"
- "When did we talk about Y?"

### activity_by_type
Filter by event type. Use for:
- "How many times did you wake up today?" (type: executive_wake)
- "What messages did you send?" (type: action)
- "What queries did reflexes handle?" (type: reflex)

## Answering Common Questions

### "What did you do today?"
1. Call `activity_today`
2. Summarize by category:
   - Inputs received
   - Reflexes that handled queries
   - Executive sessions (with duration)
   - Actions taken

### "Why did you do X?"
1. Search activity for the event: `activity_search("X")`
2. Look for context/reasoning fields
3. Check executive_wake entries around that time for context

### "How long did that take?"
1. Find the `executive_wake` entry
2. Find the matching `executive_done` entry
3. Check `duration_sec` field

### "Did you handle X?"
1. `activity_search("X")` - look for matching entries
2. Check if reflex or executive handled it

## Best Practices

- When asked about your activity, always check the activity log first
- The log is append-only and git-synced for full history
- Include timestamps when reporting activity
- Summarize rather than dump raw JSON when possible
