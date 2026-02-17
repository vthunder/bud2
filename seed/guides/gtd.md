# GTD System

The GTD (Getting Things Done) system manages the owner's personal tasks via Things 3 MCP integration. This is separate from Bud's own task queue, which also lives in Things 3 under the "Bud" area:

**User's GTD (all areas except "Bud"):**
- Areas: Work, Life, Health, etc.
- Projects and tasks for personal productivity
- Managed via `gtd_*` MCP tools

**Bud's internal systems (Things "Bud" area only):**
- Tasks: Commitments and work items (anytime/today scheduling)
- Ideas: "Ideas" project (someday) - active and explored ideas (completed items stay visible with "Show Completed")
  - Explored ideas indexed at `notes/ideas-explored.md` (title → Things ID for lookup)
- Managed via `things_*` MCP tools directly

## Core Concepts

### Areas
High-level categories of responsibility. Examples: Work, Home, Health, Finances.

```json
{
  "id": "area-abc123",
  "title": "Work"
}
```

### Projects
Multi-step outcomes belonging to an area. Projects can have headings to organize tasks within them.

```json
{
  "id": "proj-xyz789",
  "title": "Launch new product",
  "notes": "Q1 priority",
  "when": "anytime",
  "area": "area-abc123",
  "headings": ["Research", "Design", "Build", "Launch"],
  "status": "open",
  "order": 1234567890
}
```

**When values for projects:**
- `anytime` - Active, can work on now
- `someday` - Deferred, not active
- `YYYY-MM-DD` - Scheduled to start on date

### Tasks
Individual actions. Can belong to a project (and heading within it), or directly to an area.

```json
{
  "id": "task-def456",
  "title": "Draft product spec",
  "notes": "Focus on MVP features",
  "checklist": [
    {"text": "User flows", "done": true},
    {"text": "API design", "done": false}
  ],
  "when": "today",
  "project": "proj-xyz789",
  "heading": "Research",
  "area": "",
  "repeat": "",
  "status": "open",
  "completed_at": null,
  "order": 1234567890
}
```

**When values for tasks:**
- `inbox` - Uncategorized, needs processing
- `today` - Do today
- `anytime` - Available to work on
- `someday` - Deferred
- `YYYY-MM-DD` - Scheduled for specific date

**Status values:**
- `open` - Active
- `completed` - Done
- `canceled` - Won't do

## Repeating Tasks

Tasks can repeat automatically when completed. Set `repeat` to one of:
- `daily`
- `weekly`
- `biweekly`
- `monthly`
- `quarterly`
- `yearly`

When a repeating task is completed:
1. The current task is marked completed
2. A new task is created with the same properties
3. If `when` was a date, the next occurrence date is calculated
4. Checklists are reset (all items unchecked)

## MCP Tools

### gtd_add
Add a task to the user's GTD system.

**Parameters:**
- `title` (required) - What needs to be done
- `notes` - Additional context
- `when` - inbox (default), today, anytime, someday, or YYYY-MM-DD
- `project` - Project ID to add task to
- `heading` - Heading name within the project (requires project)
- `area` - Area ID (only if not in a project)

**Examples:**
```
gtd_add title="Buy groceries"                     # Quick capture to inbox
gtd_add title="Review PR" when="today"            # Add to today
gtd_add title="Write tests" project="proj-123"   # Add to project
```

### gtd_list
List tasks with optional filters. By default, only shows open tasks.

**Parameters:**
- `when` - Filter by when: inbox, today, anytime, someday, logbook, or YYYY-MM-DD
- `project` - Filter by project ID
- `area` - Filter by area ID
- `status` - open (default), completed, canceled, or all

The special `when=logbook` value shows completed and canceled tasks (like Things' Logbook view).

**Examples:**
```
gtd_list when="today"                # Today's open tasks
gtd_list when="inbox"                # Inbox tasks (open only)
gtd_list when="logbook"              # Completed + canceled tasks
gtd_list project="proj-123"          # Tasks in project (open only)
gtd_list status="all"                # All tasks regardless of status
```

### gtd_update
Update an existing task.

**Parameters:**
- `id` (required) - Task ID to update
- `title` - New title
- `notes` - New notes
- `when` - New when value
- `project` - Move to project (empty string to remove)
- `heading` - Set heading within project
- `area` - Set area (empty string to remove)
- `checklist` - Array of {text, done} objects

**Examples:**
```
gtd_update id="task-123" when="today"             # Move to today
gtd_update id="task-123" project=""               # Remove from project
gtd_update id="task-123" checklist=[{"text": "Step 1", "done": true}]
```

### gtd_complete
Mark a task as complete. Handles repeating tasks automatically.

**Parameters:**
- `id` (required) - Task ID to complete

### gtd_areas
Manage areas of responsibility.

**Parameters:**
- `action` (required) - list, add, or update
- `id` - Area ID (required for update)
- `title` - Area title (required for add)

**Examples:**
```
gtd_areas action="list"                           # List all areas
gtd_areas action="add" title="Health"             # Create area
gtd_areas action="update" id="area-123" title="Wellness"  # Rename
```

### gtd_projects
Manage projects. By default, list only shows open projects.

**Parameters:**
- `action` (required) - list, add, or update
- `id` - Project ID (required for update)
- `title` - Project title (required for add)
- `notes` - Project notes
- `when` - anytime, someday, or YYYY-MM-DD
- `area` - Area ID for filtering (list) or assignment (add/update)
- `status` - Filter for list: open (default), completed, canceled, or all
- `headings` - Ordered list of heading names

**Examples:**
```
gtd_projects action="list"                        # List open projects
gtd_projects action="list" status="all"           # List all projects
gtd_projects action="list" area="area-123"        # Open projects in area
gtd_projects action="add" title="New Feature" area="area-123"
gtd_projects action="update" id="proj-123" headings=["Todo", "Done"]
```

## Typical Workflows

### Quick Capture
Capture a thought without organizing:
```
gtd_add title="Look into that thing John mentioned"
```
Task goes to inbox for later processing.

### Morning Review
Check what's on for today:
```
gtd_list when="today"
```

### Weekly Review
Process inbox and review projects:
```
gtd_list when="inbox"          # Process each item
gtd_projects action="list"     # Review active projects
gtd_list status="completed"    # See what got done
```

### Setting Up a New Project
```
gtd_areas action="add" title="Side Project"
gtd_projects action="add" title="Build App" area="area-xxx" headings=["Design", "Build", "Test"]
gtd_add title="Sketch wireframes" project="proj-xxx" heading="Design" when="anytime"
```
