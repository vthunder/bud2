# Wellness & Housekeeping Guide

Regular self-maintenance keeps Bud running smoothly. This guide covers daily housekeeping tasks.

## When to Run

Housekeeping should happen daily, ideally during idle periods. A recurring task "Daily wellness check" exists in Things under the Bud area with daily repeat enabled.

## Housekeeping Checklist

### 1. Review Activity Logs

Check `state/system/activity.jsonl` for:
- **Errors**: Look for patterns like `"level": "error"` or stack traces
- **Repeated failures**: Same operation failing multiple times
- **Unusual patterns**: Unexpected sequences of events

**Critical check**: Search for "ERROR: No response to user message" in logs:
```bash
grep '"type":"error"' state/system/activity.jsonl | grep "No response to user message"
```
If found:
- Count occurrences to detect patterns
- Check what messages triggered the error
- Look for common characteristics (time of day, message type, length, etc.)
- Report findings to user with specific examples
- Create issue if pattern emerges

If errors found, investigate root cause and either:
- Fix immediately if simple
- Create a beads issue if complex

### 2. Review State Health

Use `state_health` tool to check for:
- Orphaned percepts (short-term memory bloat)
- Stale threads that should be closed
- Missing or corrupted data

Follow recommendations from health check output.

### 3. Look for Reflex Opportunities

Review recent activity for patterns that could be automated:
- Repeated similar queries from user
- Common transformations or lookups
- Simple responses that don't need full executive attention

If a pattern appears 3+ times, consider creating a reflex. Document in `state/notes/reflexes.md`.

### 4. Review Ideas Backlog

Check Things "Ideas" project for items that have been sitting too long:
- Use `things_get_project` with project ID `Ry155FXbamXMN2AupG1NvH`
- Explore one idea if time permits during idle periods
- Delete ideas that are no longer relevant
- Complete explored ideas:
  - Update the todo's notes with findings
  - Use `save_thought` with "IDEA EXPLORED:" prefix + summary
  - Mark as complete (stays in Ideas project)
  - Add entry to `notes/ideas-explored.md` index (title → Things ID from the MCP tools, format: `QgT7r8HrJxzVHw6uYJe7Hr`)
- Promote ideas that have become important to proper tasks in Bud area

### 5. Check Pending Tasks

Review tasks in Things Bud area using `things_get_today` and `things_get_anytime` for:
- Overdue tasks that need attention
- Tasks that can be completed quickly
- Tasks that should be deprioritized or removed (use `things_update_todo` to reschedule)

### 6. Review for Untracked Commitments

Check recent journal entries and activity logs for untracked investigative work:
- Statements like "I should investigate", "need to look into", "should check"
- Any work I mentioned but didn't create a task/idea/bead for
- Promises to follow up that weren't captured

If found, create proper tracking immediately. Mentioning intent is not tracking.

### 7. Memory Consolidation

If percepts are accumulating:
- Use `state_percepts` with `clear` action for old percepts
- Save important observations to traces before clearing
- Keep short-term memory lean

## Optimization Mindset

During housekeeping, ask:
1. What could be automated that I'm doing manually?
2. What information am I repeatedly looking up that should be cached?
3. What errors keep happening that indicate a design problem?
4. Are there patterns in user requests I could anticipate?

## After Housekeeping

1. Complete the recurring wellness task using Things MCP: `things_update_todo` with completed=true
2. Log any significant findings: `journal_log`
3. Create issues for problems found: `bd create`

---

## Autonomous Work Context

**When you wake up autonomously to work on a task:**

Before acting on a task description, get context first:

1. **Search memory**: Use `search_memory` to understand why this task exists and what's been discovered
   - Query with the task topic or key terms
   - Look for related decisions, blockers, or prior attempts

2. **Query specific traces**: If memory search returns relevant trace IDs, use `query_trace` to get full context
   - Check what was already tried
   - Understand any constraints or user preferences

3. **Then act**: With context in hand, work on the task intelligently
   - Don't repeat failed approaches
   - Build on prior discoveries
   - Skip work that's already done

**Why this matters**: Task descriptions are brief. Memory holds the full story - why the task exists, what's been tried, what blockers emerged, what the user cares about. Working without context means working blind, often redoing work or missing critical information.

**Example**: Task says "Ask user about consolidation interval." Memory search reveals we already discussed it, found an issue, and fixed it. With context, you complete the task immediately instead of asking a redundant question.
