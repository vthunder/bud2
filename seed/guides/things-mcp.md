# Things 3 MCP Integration

Two separate tool families exist for Things 3. Understanding when to use each is important.

## Two Tool Families

### `gtd_*` tools — User's personal GTD system

Manages the owner's personal tasks via a higher-level abstraction. Use these when the user asks you to:
- Add a task, capture a thought, or manage their to-dos
- Look up or update items in their Work/Life/etc. areas
- Work with their projects and scheduled tasks

Key tools: `gtd_add`, `gtd_list`, `gtd_update`, `gtd_complete`, `gtd_areas`, `gtd_projects`

The gtd_* tools have their own areas/projects data model. Full docs: `guides/gtd.md`

### `things_*` tools — Direct Things 3 access

Used for Bud's internal task queue and idea backlog (in the "Bud" area of Things), plus anything requiring direct Things 3 operations not covered by `gtd_*`.

Key tools: see below.

**Rule of thumb:** If the user asks to manage their tasks → use `gtd_*`. If managing Bud's own tasks/ideas → use `things_*`.

---

## things_* Tool Reference

### Reading / Listing

| Tool | Returns |
|------|---------|
| `things_get_inbox` | Uncategorized tasks |
| `things_get_today` | Tasks scheduled for today |
| `things_get_upcoming` | Tasks with future scheduled dates |
| `things_get_anytime` | Tasks available anytime (no date) |
| `things_get_someday` | Deferred tasks |
| `things_get_logbook` | Completed tasks (history) |
| `things_get_trash` | Deleted tasks |
| `things_get_projects` | All active projects |
| `things_get_areas` | All areas |
| `things_get_tags` | All tags |
| `things_get_project(project_id)` | Todos in a specific project |
| `things_get_area(area_id)` | Items in a specific area |
| `things_get_list(list)` | List by name: "inbox", "today", "anytime", etc. |
| `things_get_todo_details(id)` | Full details: notes, checklist, deadline, status |

### Creating

| Tool | Use |
|------|-----|
| `things_add_todo` | Add a task to any list/project/area |
| `things_add_project` | Create a project with optional headings/items |
| `things_add_items_to_project` | Append todos/headings to existing project |

### Updating

| Tool | Use |
|------|-----|
| `things_update_todo` | Update title, notes, when, status, checklist, tags |
| `things_update_project` | Update project metadata |

### Navigation (UI)

| Tool | Use |
|------|-----|
| `things_show(query)` | Open a built-in list in Things app: "inbox", "today", "anytime", "upcoming", "someday", "logbook", "trash" |
| `things_show(id)` | Navigate to a specific todo/project/area |

---

## Bud's Internal Areas

```
Bud area (K2v9QVdae4jBuTy9VSDvYc)
├── Ideas project (Ry155FXbamXMN2AupG1NvH) — someday, ideas backlog
└── Engram project (SgFXR3Y3Hb24j5m1erovZ8) — Engram-related work
```

When creating tasks for Bud's own use:
```python
things_add_todo(title="...", list="Bud", when="today")      # Scheduled task
things_add_todo(title="...", list_id="Ry155FXbamXMN2AupG1NvH")  # Add to Ideas
```

---

## Common Patterns

### Find an idea by keyword
1. Read `notes/ideas-explored.md` to scan titles
2. Get the Things ID from the matching entry
3. `things_get_todo_details(id)` for full notes

### Create a task with context
```python
things_add_todo(
    title="Review PR #123",
    list="Bud",
    when="today",
    notes="PR from @alice. Context: refactoring auth module. File: state/projects/..."
)
```

### Add a project with structure
```python
things_add_project(
    title="Onboarding flow redesign",
    area="Work",
    items=[
        {"type": "heading", "title": "Research"},
        {"type": "todo", "title": "Review current flow"},
        {"type": "heading", "title": "Design"},
        {"type": "todo", "title": "Sketch wireframes"},
    ]
)
```

### Schedule a task
`when` values: `"today"`, `"tomorrow"`, `"evening"`, `"anytime"`, `"someday"`, `"YYYY-MM-DD"`, `"YYYY-MM-DD@HH:MM"`

---

## Notes Policy

Always add notes when creating Things items. Include:
- Why the task exists (trigger, context)
- Links to relevant files (`state/projects/...`, issue numbers)
- Outcome summary when completing

A task title alone is not enough context for future reference.

---

## Completing Tasks

No dedicated `things_complete_todo` tool exists. Mark complete via update:
```python
things_update_todo(id="...", title="...", completed=True)
```

For repeating tasks managed via `gtd_*`: use `gtd_complete(id)` which handles next-occurrence creation automatically.
