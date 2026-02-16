# Internal Systems

This documents the internal systems I use to manage my work.

## Tasks (Things "Bud" Area)

Tasks are commitments - things I've promised to do. My tasks are stored in Things 3 under the "Bud" area, using the Things MCP integration.

**Priority levels:**
- 1 = highest priority, do first
- 2 = medium priority (default)
- 3 = low priority, do when time permits

**Tools:**
- Things MCP tools (see system/guides/things-mcp.md for full documentation):
  - `things_add_todo` - create a new task in the Bud area
  - `things_get_today` - see tasks scheduled for today
  - `things_get_anytime` - see tasks available anytime
  - `things_update_todo` - update or reschedule a task
  - `things_get_logbook` - see completed tasks

**Organization:**
- All Bud tasks belong to the "Bud" area in Things
- Use `when` parameter to schedule: "today", "anytime", "someday", or specific dates
- Store task metadata (original ID, priority, context) in the notes field
- Recurring tasks use Things' built-in repeat functionality

Overdue tasks generate high-intensity impulses that wake me up.

## Ideas (Things "Ideas" Project)

Ideas are things I want to explore someday - not commitments, just curiosities. They're for learning and exploration. When exploration reveals actionable work, that graduates to a task or beads issue.

Ideas are stored as todos in a single Things 3 project called "Ideas" in the "Bud" area (scheduled as "Someday"). Active and explored ideas live in the same project - toggle "Show Completed" in the UI to see both.

**Project ID:** `Ry155FXbamXMN2AupG1NvH`

**Structure:**
- Each idea is a todo in the "Ideas" project
- Title: Brief description of the idea
- Notes: Include context like:
  - What sparked this idea
  - Why it's interesting
  - Any initial thoughts or questions

**Tools:**
- `things_add_todo` - create new idea (use `list_id: "Ry155FXbamXMN2AupG1NvH"`)
- `things_get_project` - list all ideas (active + completed if in logbook)
- `things_update_todo` - update idea details or notes
- `things_complete_todo` - mark idea as explored

**Lifecycle:**
1. **Capture**: Add idea as todo to "Ideas" project with context in notes
2. **Explore**: During idle time, research and think through the idea
3. **Record outcome**: Update notes with findings before marking complete:
   - **Actionable**: Discovered work → create task/beads issue, reference in notes
   - **Interesting**: Learned something useful, document in notes
   - **Not useful**: Dead end, note why
   - **Deferred**: Worth revisiting later, note blockers
4. **Save thought**: Use `save_thought` with "IDEA EXPLORED:" prefix + summary of exploration and findings
5. **Complete**: Mark todo as complete (stays in Ideas project, visible when showing completed items)
6. **Update index**: Add entry to `notes/ideas-explored.md` index mapping idea title → **Things ID** (use the ID returned by Things MCP tools, format: `QgT7r8HrJxzVHw6uYJe7Hr`, NOT the old `idea-` format)
7. **Track follow-up**: If actionable work created, track task/beads to completion

**Explored ideas index:**
A lightweight index is maintained at `notes/ideas-explored.md` that maps idea titles to their Things IDs for quick lookup. This enables searching for relevant past explorations by keywords, then retrieving full details via `things_get_todo_details` with the ID.

**IMPORTANT:** The index uses **Things IDs** (format: `QgT7r8HrJxzVHw6uYJe7Hr`), which are the system-generated IDs returned by Things MCP tools. These are different from the old `idea-XXXXXXX` format IDs.

**Retrieval workflow:**
1. Search the index file (`notes/ideas-explored.md`) for relevant keywords
2. Get the Things ID from the matching entry (format: `QgT7r8HrJxzVHw6uYJe7Hr`)
3. Use `things_get_todo_details(id)` to retrieve full exploration notes
4. The `save_thought` memory traces provide semantic search across explorations

The index is NOT a duplicate of findings - it's a lookup table. Full exploration details live in the Things todo notes.

Ideas only generate impulses during idle time (low intensity).

## Impulses

Impulses are internal motivations (vs percepts which are external).

**Sources:**
- Task due or overdue → high intensity
- Idea to explore → low intensity (idle only)
- Scheduled item → medium intensity
- System wake → medium intensity

Impulses and percepts are scored together by attention. User messages naturally get high salience, so autonomous work yields to user interaction.
