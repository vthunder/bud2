# Memory Introspection Guide

Daily procedure to maintain memory quality and identify system improvements.

## Goals

1. **Maintain memory quality** - prune bad data that degrades retrieval
2. **Improve the system** - identify root causes so bad data isn't created

## Where to Find Things

- **This guide**: `state/system/guides/memory-introspection.md`
- **Health report & insights**: `state/notes/memory-health.md`
- **Memory database**: `state/system/memory.db`
- **Task**: Daily introspection task in Things Bud area (recurring daily)

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

# Recent traces (check for quality issues)
# NOTE: traces.summary is always empty - summaries stored in trace_summaries table
sqlite3 ... "SELECT t.id, ts.summary FROM traces t
JOIN trace_summaries ts ON t.id = ts.trace_id
WHERE ts.compression_level = 8
ORDER BY t.created_at DESC LIMIT 10;"

# PERSON entities (are names being captured?)
sqlite3 ... "SELECT name FROM entities WHERE type = 'PERSON';"
```

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
- **Bugs found** → `things_add_todo` in Bud area with context in notes
- **System improvements** → `things_add_todo` in "Ideas" project (`list_id: "Ry155FXbamXMN2AupG1NvH"`) for later exploration; when explored, add entry to `notes/ideas-explored.md`
- **Root causes** → Document in Dated Insights

## SQL Queries Reference

```sql
-- Record counts
SELECT COUNT(*) FROM episodes;
SELECT COUNT(*) FROM entities;
SELECT COUNT(*) FROM traces;
SELECT COUNT(*) FROM episode_edges;
SELECT COUNT(*) FROM trace_summaries;

-- Entity type distribution
SELECT type, COUNT(*) FROM entities GROUP BY type ORDER BY COUNT(*) DESC;

-- Trace activation distribution
SELECT ROUND(activation, 1) as bucket, COUNT(*) FROM traces GROUP BY bucket ORDER BY bucket DESC;

-- Dead traces (activation near zero)
SELECT COUNT(*) FROM traces WHERE activation < 0.05;

-- Sample PRODUCT entities
SELECT name FROM entities WHERE type = 'PRODUCT' LIMIT 20;

-- Sample PERSON entities
SELECT name FROM entities WHERE type = 'PERSON';

-- Recent traces (summaries stored in trace_summaries table)
-- L1 summary (64 tokens, most readable for quality checks)
SELECT t.id, ts.summary
FROM traces t
JOIN trace_summaries ts ON t.id = ts.trace_id
WHERE ts.compression_level = 8
ORDER BY t.created_at DESC
LIMIT 10;

-- Trace summary coverage
SELECT COUNT(DISTINCT trace_id) as traces_with_summaries,
       COUNT(*) as total_summaries,
       COUNT(DISTINCT compression_level) as compression_levels
FROM trace_summaries;

-- Compression level distribution
SELECT compression_level, COUNT(*)
FROM trace_summaries
GROUP BY compression_level
ORDER BY compression_level;
```

## Time & Frequency

- **Time estimate**: 5-10 minutes routine, longer if issues found
- **Frequency**: Daily, during idle periods
- **Task**: Daily introspection recurring task in Things Bud area
