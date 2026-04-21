---
name: things-operations
description: >-
  This skill should be used when the agent needs to interact with the issue
  tracker — creating issues, querying for ready/unblocked work, claiming tasks,
  updating status, or reading issue details. Provides Things 3 tool names,
  field mappings, and workflow patterns.
type: skill
callable_from: direct
---

# Issue Operations (Things 3)

This skill provides Things 3 MCP tool names, parameters, and workflows for all issue tracker operations. All planning work is written to the **Bud** area in Things 3.

## Prerequisites

Things 3 MCP is loaded via `mcp__bud2__things_*` tools. The default planning area is `Bud`. If no area is configured or available in context, throw a hard error — do NOT silently write to the wrong area.

```
HARD RULE: Before creating any issue, verify you have an area to write to.
If planning_area is not set or cannot be determined, stop and report:
  "things-operations: no planning area configured — set planning_area in bud.yaml"
```

Tag all planning items with `autopilot:managed` to distinguish from human-created todos.

## Query Operations

### Find unblocked work
Use `things_get_area` with the Bud area ID, then filter for items without blocking dependencies. Or use `things_get_project` to list items in a specific planning project.

### List all issues
Use `things_get_projects` to list all active projects, then `things_get_project(project_id)` to list todos within each planning project.

### Search issues
Use `things_get_list("anytime")` or `things_get_area` filtered by tag (`autopilot:managed` + type tag). For a specific item, use `things_get_todo_details(id)`.

### Get issue details
Use `things_get_todo_details(id)` to get title, notes, checklist, status, tags, deadline, and project membership.

## Write Operations

### Create an issue

Field mapping from task JSON to Things parameters:

- `title` → `title`
- `description` / `body` → `notes` (include constraints and acceptance criteria in plain text)
- `type` → tags: add `type:epic`, `type:task`, `type:bug`, `type:feature`, or `type:chore` tag
- `acceptance` → include in `notes` under an "Acceptance Criteria:" section
- `dependencies` → checklist items referencing the blocking todo title, or notes annotation
- `priority` → `when`: 0=today, 1=today, 2=anytime, 3=anytime, 4=someday

For epics, create as a **project** using `things_add_project`. Child tasks become todos within that project using `things_add_items_to_project`.

```
things_add_project(
  title="Epic: npm publishing pipeline",
  area="Bud",
  notes="Strategy direction: ...\nScope: ...\nAcceptance: ...",
  tags=["autopilot:managed", "type:epic"]
)
```

For tasks, use `things_add_todo`:
```
things_add_todo(
  title="Create strengthenOnRead helper",
  list_id=<epic_project_id>,
  notes="Goal: ...\nAcceptance Criteria:\n- bun test passes\n- helper has unit test",
  tags=["autopilot:managed", "type:task"],
  when="anytime"
)
```

### Claim a task
Set `when="today"` via `things_update_todo`. Check existing `when` first — if already set to today, the task is claimed.

### Update status

Status mapping:
- **done** → `things_update_todo(id, completed=True)`
- **blocked** → add a tag `status:blocked` and update notes with blocker explanation: `things_update_todo(id, tags=[..., "status:blocked"], notes="BLOCKED: <reason>\n\n" + existing_notes)`

## Workflow Patterns

### Epic planner workflow
1. Check `things_get_projects` for existing epics with `autopilot:managed` tag — avoid duplicates
2. After planning, create each epic as a project: `things_add_project(title, area="Bud", tags=["autopilot:managed", "type:epic"])`
3. Verify by listing projects after creating: `things_get_projects`

### Task planner workflow
1. List projects tagged `autopilot:managed` + `type:epic` to find unblocked epics
2. Read the selected epic's project to check for existing child todos
3. Create tasks as items in the epic project: `things_add_items_to_project(project_id, items=[...])`
4. Maintain a map of task ID (T1, T2...) → Things todo ID as you create
5. After all tasks created, annotate dependency ordering in each task's notes

### Executor workflow
1. Set `when="today"` on the target todo — check it isn't already claimed
2. Read todo details: `things_get_todo_details(id)` — title, notes, acceptance criteria
3. Implement the task
4. Mark done or blocked as final action

## Key Differences from Linear

- Dependencies are tracked via **notes annotations** and ordering, not issue relations. Create tasks in dependency order.
- Acceptance criteria live in the **notes** field (plain text), not a dedicated field.
- Issue type is expressed via **tags** (`type:epic`, `type:task`, etc.), not a dedicated type field.
- Claiming is **setting when=today**, not an assign + state change.
- Epics are **Things projects**; tasks are **todos within that project**.
- Use `things_add_items_to_project` to append tasks to an existing epic project.
- No native blocking relation — use ordering and notes to express dependencies.
