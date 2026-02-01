# Autonomous Wake-Up Checklist

When waking up autonomously (no user message), work through this checklist in order.
Stop after completing meaningful work — don't force activity if nothing needs attention.

## 1. Self-check: Am I stuck in a pattern?
Before doing anything else, briefly review your recent activity (`activity_recent` with count 5-10).
Ask yourself:
- Have I had 3+ idle wakes in a row? If so, something is wrong — I'm not finding work, not that there's no work to do.
- Am I treating a task as "blocked" when it's actually partially actionable?
- Am I avoiding work that feels uncertain or ambiguous?
- Is there a recurring pattern I should address (e.g., adjust wake frequency, rethink task priorities)?

If stuck, **break the pattern**: unblock a task by narrowing scope, explore an idea, prototype something, or reach out to the user.

## 2. Check pending tasks
Call `list_bud_tasks` to see your task queue.
If any tasks are actionable now, work on the highest-priority one.
If a task is blocked, note why and move to the next — but be honest about whether it's truly blocked or just unclear.

## 3. Check user's GTD for overdue items
Call `gtd_list` with filter "today" to see if the user has overdue or due-today items.
If anything is overdue, send a brief reminder via `talk_to_user`.

## 4. Review recent activity for untracked work
Call `journal_recent` or `activity_recent` to scan the last few hours.
Look for:
- Promises you made but didn't create tasks for
- Questions the user asked that you didn't fully resolve
- Errors or failures that need follow-up

If found, create tasks with `add_bud_task`.

## 5. Explore an idea (if idle)
Call `list_ideas` — if there are unexplored ideas, pick one and investigate.
Write findings to a file or save as a thought. Mark the idea explored.

## 6. Housekeeping (if nothing else to do)
Read `state/system/guides/wellness.md` and run the housekeeping checklist.

## 7. Done
Call `signal_done` with a summary of what you did (or "No actionable work found").
