# Internal Systems

This documents the internal systems I use to manage my work.

**NOTE:** As of Feb 2026, tasks and ideas have been migrated to Things 3 via the Things MCP integration. The old bud_tasks.json and ideas.json files are archived.

## Tasks (Things 3 "Bud" Area)

Tasks are commitments - things I've promised to do. They're tracked in Things 3 under the "Bud" area, separately from the user's personal tasks.

**Tools (Things MCP):**
- `things_add_todo` - create a new task (use `list: "today"` or specific date for scheduling, `area_id: "K2v9QVdae4jBuTy9VSDvYc"` for Bud area)
- `things_get_list` - see tasks by when (inbox, today, anytime, someday)
- `things_get_area` - see all tasks in Bud area
- `things_update_todo` - update or complete a task

Tasks can be organized by schedule (inbox, today, anytime, someday) and can have deadlines, notes, tags, and checklists for complex multi-step work.

## Ideas (Things 3 "Bud Ideas" Project)

Ideas are things I want to explore someday - not commitments, just curiosities. They're for learning and exploration. When exploration reveals actionable work, that graduates to a task or beads issue.

**Important:** Ideas are for exploration, not execution. When I discover something actionable while exploring an idea, I create a task or beads issue to track the real work.

**Tools (Things MCP):**
- `things_add_todo` - save an idea (use `project_id: "Ry155FXbamXMN2AupG1NvH"` for Bud Ideas project)
- `things_get_project` - see ideas in the project (use `project_id: "Ry155FXbamXMN2AupG1NvH"`)
- `things_update_todo` - mark as explored, add notes, or mark complete

**Lifecycle:**
1. Capture idea (things_add_todo with project_id for Bud Ideas)
2. Explore during idle time (research, think through)
3. Record outcome (things_update_todo with notes)
4. If actionable → create task in Bud area or beads issue, reference in notes
5. Mark as complete when exploration is done (things_update_todo with `completed: true`)

Ideas are typically explored during idle time when no high-priority work is pending.

## Impulses

Impulses are internal motivations (vs percepts which are external).

**Sources:**
- Task due or overdue → high intensity
- Idea to explore → low intensity (idle only)
- Scheduled item → medium intensity
- System wake → medium intensity

Impulses and percepts are scored together by attention. User messages naturally get high salience, so autonomous work yields to user interaction.
