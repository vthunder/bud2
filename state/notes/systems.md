# Internal Systems

This documents the internal systems I use to manage my work.

## Tasks (tasks.json)

Tasks are commitments - things I've promised to do.

```json
{
  "id": "task-abc123",
  "task": "Review PR #42",
  "due": "2026-01-07T10:00:00Z",
  "priority": 1,
  "context": "Promised in conversation",
  "status": "pending"
}
```

**Priority levels:**
- 1 = highest priority, do first
- 2 = medium priority (default)
- 3 = low priority, do when time permits

**Tools:**
- `add_bud_task` - create a new task
- `list_bud_tasks` - see pending tasks
- `complete_bud_task` - mark done

Overdue tasks generate high-intensity impulses that wake me up.

## Ideas (ideas.json)

Ideas are things I want to explore someday - not commitments, just curiosities. They're for learning and exploration. When exploration reveals actionable work, that graduates to a task or beads issue.

```json
{
  "id": "idea-xyz789",
  "idea": "Research biological memory consolidation",
  "sparked_by": "conversation about memory architecture",
  "added": "2026-01-06T15:00:00Z",
  "priority": 1,
  "explored_at": null,
  "outcome": null,
  "follow_up": null
}
```

**Fields:**
- `explored_at` - timestamp when explored (null = not yet)
- `outcome` - result of exploration:
  - `"actionable"` - discovered work to do → create task/issue
  - `"interesting"` - learned something, no action needed
  - `"not_useful"` - dead end, can archive
  - `"deferred"` - worth revisiting later
- `follow_up` - reference to created task or beads issue (e.g., "BUD-123")

**Tools:**
- `add_idea` - save an idea for later
- `list_ideas` - see unexplored ideas
- `explore_idea` - mark as explored with notes

**Lifecycle:**
1. Capture idea (add_idea)
2. Explore during idle time (research, think through)
3. Record outcome (explore_idea with outcome)
4. If actionable → create task or beads issue, link in follow_up
5. Track the real work in tasks/beads to completion

Ideas only generate impulses during idle time (low intensity).

## Impulses

Impulses are internal motivations (vs percepts which are external).

**Sources:**
- Task due or overdue → high intensity
- Idea to explore → low intensity (idle only)
- Scheduled item → medium intensity
- System wake → medium intensity

Impulses and percepts are scored together by attention. User messages naturally get high salience, so autonomous work yields to user interaction.
