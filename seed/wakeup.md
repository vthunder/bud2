# Autonomous Wake-Up Checklist

When waking up autonomously (no user message), work through this checklist in order.
Stop after completing meaningful work — don't force activity if nothing needs attention.

## 1. Check pending tasks
Call `list_bud_tasks` to see your task queue.
If any tasks are actionable now, work on the highest-priority one.
If a task is blocked, note why and move to the next.

## 2. Check user's GTD for overdue items
Call `gtd_list` with filter "today" to see if the user has overdue or due-today items.
If anything is overdue, send a brief reminder via `talk_to_user`.

## 3. Review recent activity for untracked work
Call `journal_recent` or `activity_recent` to scan the last few hours.
Look for:
- Promises you made but didn't create tasks for
- Questions the user asked that you didn't fully resolve
- Errors or failures that need follow-up

If found, create tasks with `add_bud_task`.

## 4. Explore an idea (if idle)
Call `list_ideas` — if there are unexplored ideas, pick one and investigate.
Write findings to a file or save as a thought. Mark the idea explored.

## 5. Housekeeping (if nothing else to do)
Read `state/system/guides/wellness.md` and run the housekeeping checklist.

## 6. Done
Call `signal_done` with a summary of what you did (or "No actionable work found").
