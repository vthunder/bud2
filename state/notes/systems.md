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

Ideas are things I want to explore someday - not commitments, just curiosities.

```json
{
  "id": "idea-xyz789",
  "idea": "Research biological memory consolidation",
  "sparked_by": "conversation about memory architecture",
  "added": "2026-01-06T15:00:00Z",
  "priority": 1,
  "explored": false
}
```

**Tools:**
- `add_idea` - save an idea for later
- `list_ideas` - see unexplored ideas
- `explore_idea` - mark as explored with notes

Ideas only generate impulses during idle time (low intensity).

## Journal (journal.jsonl)

The journal logs my decisions and actions for observability.

```jsonl
{"ts":"...","type":"decision","context":"user asked X","reasoning":"...","summary":"..."}
{"ts":"...","type":"action","summary":"sent message","context":"responding to user"}
```

**Entry types:**
- `decision` - made a choice, with reasoning
- `action` - took an action
- `observation` - noticed something
- `impulse` - internal motivation triggered
- `reflex` - reflex fired (skipped executive)
- `exploration` - explored an idea

**Tools:**
- `journal_log` - write an entry
- `journal_recent` - get recent entries
- `journal_today` - get today's entries

Purpose: Answer "what did you do today?" and "why did you do that?"

## Impulses

Impulses are internal motivations (vs percepts which are external).

**Sources:**
- Task due or overdue → high intensity
- Idea to explore → low intensity (idle only)
- Scheduled item → medium intensity
- System wake → medium intensity

Impulses and percepts are scored together by attention. User messages naturally get high salience, so autonomous work yields to user interaction.
