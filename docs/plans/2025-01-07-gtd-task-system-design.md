# GTD Task System Design

## Overview

Add a GTD-style task management system for the owner's tasks, separate from Bud's commitments. Bud replaces Things as the primary task manager.

## Key Concepts

**Two task systems:**
- **bud_tasks**: Bud's commitments - things Bud has promised to do. Bud is accountable.
- **user_tasks (GTD)**: Owner's tasks - Bud assists, reminds, and can proactively help, but owner is accountable.

## Data Model

### Task
```json
{
  "id": "string",
  "title": "string",
  "notes": "string (freeform)",
  "checklist": [{"text": "string", "done": false}],
  "when": "inbox | today | anytime | someday | 2024-01-15",
  "project": "string (project ID, optional)",
  "heading": "string (heading name within project, optional)",
  "area": "string (area ID, optional - only if not in project)",
  "repeat": "string (optional - daily, weekly, monthly, etc.)",
  "status": "open | completed | canceled",
  "completed_at": "timestamp",
  "order": 1.0
}
```

### Project
```json
{
  "id": "string",
  "title": "string",
  "notes": "string",
  "when": "anytime | someday | 2024-01-15",
  "area": "string (area ID)",
  "headings": ["string (ordered list of heading names)"],
  "status": "open | completed | canceled",
  "order": 1.0
}
```

### Area
```json
{
  "id": "string",
  "title": "string (e.g., Work, Life)"
}
```

### Validation Rules (enforced in code)
- `when: inbox` → project/area/heading must be null
- `project` set → area comes from project, task.area ignored
- `heading` set → must have a project
- `heading` value → must exist in project.headings

## Storage

```
state/
  bud_tasks.json      # Bud's commitments (renamed from tasks.json)
  user_tasks.json     # Owner's GTD tasks
```

**user_tasks.json structure:**
```json
{
  "areas": [...],
  "projects": [...],
  "tasks": [...]
}
```

## MCP Tools

### Renamed (bud_tasks)
- `add_task` → `add_bud_task`
- `list_tasks` → `list_bud_tasks`
- `complete_task` → `complete_bud_task`

### New (GTD)
| Tool | Description |
|------|-------------|
| `gtd_add` | Quick capture to inbox, or specify when/project |
| `gtd_list` | List tasks with filters (when, project, area) |
| `gtd_update` | Move task, edit fields, change when |
| `gtd_complete` | Mark done (creates next occurrence for repeating) |
| `gtd_projects` | List/create/update projects |
| `gtd_areas` | List/create areas |

### Example Usage
```
"Add buy milk to inbox" → gtd_add(title: "buy milk")
"Move it to today" → gtd_update(id: "...", when: "today")
"What's on today?" → gtd_list(when: "today")
"Show Work projects" → gtd_projects(area: "work")
```

## Bud's Autonomous Behaviors

### Scheduled Reviews (via existing scheduled_tasks)
- **Morning review**: Show Today list, surface scheduled tasks arriving today
- **Evening wrap-up**: Review incomplete Today items, offer to reschedule
- **Weekly review**: Prompt to review Someday, clean up projects

### Impulse Generation
- Today tasks untouched for hours → gentle nudge (intensity: 0.5)
- Scheduled task date arrives → move to Today, notify (intensity: 0.6)
- Lower intensity than bud_tasks (these are nudges, not obligations)

### Proactive Help Policy
**Just do it (no permission needed):**
- Research, prep work, analysis
- Drafting documents
- Anything internal/reversible

**Ask first:**
- Modifying external documents
- Publishing or sending anything
- Actions with external side effects
- Spending money

Example: Bud sees "Research competitors for X" on Today → starts researching, reports back without asking.

## Implementation Plan

### Phase 1: Rename bud_tasks
1. Rename `state/tasks.json` → `state/bud_tasks.json`
2. Update TaskStore path
3. Rename MCP tools: `add_task` → `add_bud_task`, etc.
4. Update core_seed.md references

### Phase 2: GTD Data Layer
1. Create `internal/gtd/` package
2. Implement Area, Project, Task structs
3. Implement GTDStore with Load/Save
4. Add validation rules

### Phase 3: GTD MCP Tools
1. Implement `gtd_add`, `gtd_list`, `gtd_update`, `gtd_complete`
2. Implement `gtd_projects`, `gtd_areas`
3. Register in MCP server

### Phase 4: Autonomous Integration
1. Add GTD guide to core memory (review behaviors, help policy)
2. Create scheduled tasks for morning/evening/weekly reviews
3. Add impulse generation for Today tasks and scheduled arrivals

### Phase 5: Migration
1. Manual migration from Things (owner exports and tells Bud)
2. Or: Bud helps capture tasks conversationally

## Files to Create/Modify

### New Files
- `internal/gtd/store.go` - GTDStore, data types
- `internal/gtd/validation.go` - Business rules
- `internal/gtd/impulses.go` - Impulse generation for GTD
- `state/user_tasks.json` - Initial empty structure

### Modified Files
- `internal/motivation/tasks.go` - Update path to bud_tasks.json
- `internal/mcp/server.go` - Rename tools, add GTD tools
- `cmd/bud-mcp/main.go` - Wire up GTD store and tools
- `state/core_seed.md` - Add GTD guide section
