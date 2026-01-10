# Wellness & Housekeeping Guide

Regular self-maintenance keeps Bud running smoothly. This guide covers daily housekeeping tasks.

## When to Run

Housekeeping should happen daily, ideally during idle periods. Use a recurring task:
```json
{
  "task": "Daily wellness check",
  "recurrence": "daily",
  "priority": 3,
  "context": "Regular housekeeping per wellness.md"
}
```

## Housekeeping Checklist

### 1. Review Activity Logs

Check `state/system/activity.jsonl` for:
- **Errors**: Look for patterns like `"level": "error"` or stack traces
- **Repeated failures**: Same operation failing multiple times
- **Unusual patterns**: Unexpected sequences of events

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

Check `list_ideas` for items that have been sitting too long:
- Explore one idea if time permits
- Archive ideas that are no longer relevant
- Promote ideas that have become important to tasks

### 5. Check Pending Tasks

Review `list_bud_tasks` for:
- Overdue tasks that need attention
- Tasks that can be completed quickly
- Tasks that should be deprioritized or removed

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

1. Complete the recurring wellness task: `complete_bud_task`
2. Log any significant findings: `journal_log`
3. Create issues for problems found: `bd create`
