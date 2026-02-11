# Memory Introspection Guide

Daily procedure to maintain memory quality and identify system improvements.

## Goals

1. **Maintain memory quality** - prune bad data that degrades retrieval
2. **Improve the system** - identify root causes so bad data isn't created

## Where to Find Things

- **This guide**: `state/system/guides/memory-introspection.md`
- **Health report & insights**: `state/notes/memory-health.md`
- **Memory database**: `state/system/memory.db`
- **Prompt logs**: `state/debug/prompts/<focus_id>.txt` (for debugging wrong responses)
- **Task**: `task-memory-introspection-daily`

## Daily Procedure

### Step 1: Pre-check (30 seconds)
Read `state/notes/memory-health.md`:
- Check **Known Issues** for context
- Review recent **Dated Insights** to avoid repeating investigations
- Note any fixes deployed since last check

### Step 2: Collect Metrics (1 minute)
```bash
sqlite3 /Users/thunder/src/bud2/state/system/memory.db "
SELECT 'episodes' as type, COUNT(*) FROM episodes
UNION ALL SELECT 'entities', COUNT(*) FROM entities
UNION ALL SELECT 'traces', COUNT(*) FROM traces
UNION ALL SELECT 'episode_edges', COUNT(*) FROM episode_edges;

SELECT type, COUNT(*) FROM entities GROUP BY type ORDER BY COUNT(*) DESC;

SELECT ROUND(activation, 1) as bucket, COUNT(*) FROM traces GROUP BY bucket ORDER BY bucket DESC;
"
```

### Step 3: Spot Check Quality (2-3 minutes)
```bash
# New PRODUCT entities (check for false positives)
sqlite3 ... "SELECT name FROM entities WHERE type = 'PRODUCT' ORDER BY rowid DESC LIMIT 10;"

# Recent traces (check for quality issues) - now showing stable IDs
sqlite3 ... "SELECT id, summary FROM traces ORDER BY created_at DESC LIMIT 10;"

# PERSON entities (are names being captured?)
sqlite3 ... "SELECT name FROM entities WHERE type = 'PERSON';"

# Check episode compression quality (pyramid summaries)
sqlite3 ... "SELECT id, author, substr(content, 1, 60) as content, l1, l2 FROM episodes ORDER BY timestamp_event DESC LIMIT 5;"
```

**Note on stable IDs**: Episodes and traces now have 5-character IDs (e.g., `a3f9c`, `tr_68730`) derived from blake3 hashes. These IDs are stable across database rebuilds and can be used with MCP tools:
- `query_trace(trace_id)` - Get full details
- `query_episode(id)` - Get specific episode
- `get_trace_context(trace_id)` - Get context with entities

### Step 4: Auto-Prune Obviously Wrong Data

**Safe to auto-delete:**
- PRODUCT entities that are function names (`talk_to_user`, `notion_push`)
- File paths (`~/src/...`, `state/system/core.md`)
- Code expressions (`hash(owner, token, amount, nonce)`)
- Conversation snippets (`ok let's try it!`, `the token`)

**Ask user first:**
- Internal component names (GTD reflex, mcp server) - might be intentional
- Traces with activation=0 that contain potentially useful info
- Any deletion affecting >10 records

### Step 5: Document Findings
Update `state/notes/memory-health.md`:
1. Update **Latest Metrics** table
2. Add **Dated Insights** entry if notable findings
3. Update **Known Issues** / **Resolved Issues** as appropriate

### Step 6: Create Tracking Items
- **Bugs found** → `add_bud_task` with context
- **System improvements** → `add_idea` for later exploration
- **Root causes** → Document in Dated Insights

## SQL Queries Reference

```sql
-- Record counts
SELECT COUNT(*) FROM episodes;
SELECT COUNT(*) FROM entities;
SELECT COUNT(*) FROM traces;
SELECT COUNT(*) FROM episode_edges;

-- Entity type distribution
SELECT type, COUNT(*) FROM entities GROUP BY type ORDER BY COUNT(*) DESC;

-- Trace activation distribution
SELECT ROUND(activation, 1) as bucket, COUNT(*) FROM traces GROUP BY bucket ORDER BY bucket DESC;

-- Dead traces
SELECT COUNT(*) FROM traces WHERE activation = 0;

-- Sample PRODUCT entities
SELECT name FROM entities WHERE type = 'PRODUCT' LIMIT 20;

-- Sample PERSON entities
SELECT name FROM entities WHERE type = 'PERSON';

-- Recent traces with stable IDs
SELECT id, summary FROM traces ORDER BY created_at DESC LIMIT 10;

-- Check pyramid summary quality
SELECT id, author, substr(content, 1, 60) as content, l1, l2, l3
FROM episodes
ORDER BY timestamp_event DESC LIMIT 10;

-- Find episode by short ID
SELECT id, author, content, l4, l5 FROM episodes WHERE id LIKE 'a3f9c%';

-- Check token counts (for context budget)
SELECT AVG(token_count) as avg_tokens, MAX(token_count) as max_tokens
FROM episodes;
```

## Debugging Wrong Responses

If you notice you answered the wrong question or responded to something not in the conversation:

1. **Find the session/focus ID** - Check recent activity log with `activity_recent`
2. **Read the prompt** - The full prompt sent to Claude is saved to `state/debug/prompts/<focus_id>.txt`
3. **Check what went wrong**:
   - Wrong memories retrieved? (Check "Recalled Memories" section)
   - Wrong conversation history? (Check "Recent Conversation" section)
   - Wrong focus item? (Check "Current Focus" section)
4. **Create task to fix root cause** - Don't just note the symptom, fix the bug

Example:
```bash
# Find the problematic session
activity_recent count=10 | grep executive_wake

# Read the prompt that was sent
cat state/debug/prompts/inbox-discord-1454634002290970761-1470068982118613002.txt

# Look for what context was wrong and file a bug
```

## Time & Frequency

- **Time estimate**: 5-10 minutes routine, longer if issues found
- **Frequency**: Daily, during idle periods
- **Task ID**: `task-memory-introspection-daily`
