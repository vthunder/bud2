# External Integrations

I can query and interact with external systems using MCP tools. Each integration has specific tools and patterns.

## General Pattern

1. **Search/List** - Find items by query or filter
2. **Get** - Retrieve details for a specific item
3. **Query** - Structured queries with filters and sorting
4. **Create/Update** - Modify data (where supported)

## Notion

Sync Notion pages as markdown files using `efficient-notion-mcp`.

### Setup

The efficient-notion-mcp server is configured in `.mcp.json`:
```json
{
  "notion": {
    "command": "/Users/thunder/src/bud2/bin/efficient-notion-mcp",
    "args": [],
    "env": { "NOTION_API_KEY": "${NOTION_API_KEY}" }
  }
}
```

Create an integration at https://notion.so/profile/integrations and share pages with it.

### Available Tools

| Tool | Purpose |
|------|---------|
| `notion_pull` | Download a Notion page as markdown with frontmatter |
| `notion_push` | Upload markdown file to Notion (erase+replace) |
| `notion_diff` | Compare local markdown against live Notion content |
| `notion_query` | Query a database with filters, returns flattened JSON |
| `notion_schema` | Get database property names and types |

### Pull a Page

Downloads a Notion page and saves it as a markdown file with frontmatter:

```
notion_pull(page_id="abc123", output_dir="/tmp/notion")
```

**Output file format:**
```markdown
---
notion_id: abc123
title: My Page Title
pulled_at: 2025-01-15T10:30:00-08:00
---

# Page content here...

---

## Comments

> **Author Name** *(Jan 15, 2025)*: Comment text here
```

The `output_dir` is optional (defaults to `/tmp/notion`).

### Push a Page

Uploads a local markdown file to Notion. The file must have `notion_id` in its frontmatter:

```
notion_push(file_path="/tmp/notion/My Page Title.md")
```

**How it works:**
1. Reads the markdown file
2. Erases all existing content on the Notion page (single API call)
3. Converts markdown to Notion blocks
4. Appends blocks in batches of 100

This is much faster than block-by-block updates.

### Diff a Page

Compares local markdown against the current Notion page content:

```
notion_diff(file_path="/tmp/notion/My Page Title.md")
```

Returns a diff showing what would change if you pushed.

### Query a Database

Query a Notion database with optional filters and sorts. Returns flattened JSON (not Notion's verbose nested format):

```
notion_query(
  database_id="15ae67c666dd8073b484d1b4ccee3080",
  filter={"property": "Status", "status": {"equals": "Active Thread"}},
  sorts=[{"property": "Priority", "direction": "ascending"}],
  limit=50
)
```

**Example response:**
```json
{
  "results": [
    {"_id": "abc123", "Name": "Project Alpha", "Status": "Active Thread", "Priority": "P0"},
    {"_id": "def456", "Name": "Project Beta", "Status": "Active Thread", "Priority": "P1"}
  ],
  "has_more": false
}
```

**Filter examples:**
- Status: `{"property": "Status", "status": {"equals": "Done"}}`
- Text contains: `{"property": "Name", "title": {"contains": "urgent"}}`
- Checkbox: `{"property": "Archived", "checkbox": {"equals": false}}`
- Compound: `{"and": [filter1, filter2]}`

### Get Database Schema

Get property names and types for a database:

```
notion_schema(database_id="15ae67c666dd8073b484d1b4ccee3080")
```

**Example response:**
```json
[
  {"name": "Name", "type": "title"},
  {"name": "Status", "type": "status"},
  {"name": "Priority", "type": "select"},
  {"name": "Due Date", "type": "date"}
]
```

### Workflow Example

```
# 1. Pull page to edit locally
notion_pull(page_id="abc123")
# → Saved to /tmp/notion/Project Notes.md

# 2. Edit the file (or have Claude edit it)
# ... make changes ...

# 3. Check what changed
notion_diff(file_path="/tmp/notion/Project Notes.md")

# 4. Push changes back
notion_push(file_path="/tmp/notion/Project Notes.md")
```

### Tips

- **Page IDs from URLs:** `notion.so/.../Page-Title-abc123` → use `abc123`
- **Markdown conversion:** All Notion↔markdown conversion happens inside the MCP server
- **Comments:** Page-level comments are included when pulling (block-level comments are not currently supported)
- **Share pages with integration** - the integration can only access pages explicitly shared with it

## Google Calendar

Query and manage calendar events.

### Tools Overview

| Tool | Purpose |
|------|---------|
| `calendar_today` | Get today's events |
| `calendar_upcoming` | Get events in next N hours/days |
| `calendar_list_events` | Query events in date range |
| `calendar_free_busy` | Check availability |
| `calendar_get_event` | Get single event details |
| `calendar_create_event` | Create new event |

### Common Patterns

**Today's schedule:**
```
calendar_today()
# Returns compact format: "10:00-11:00 Team Standup (Google Meet)"
```

**Upcoming events:**
```
calendar_upcoming(duration="7d", max_results=10)
# Events in next 7 days
```

**Check availability:**
```
calendar_free_busy(time_min="2025-01-15", time_max="2025-01-16")
# Shows busy periods
```

**Create event:**
```
calendar_create_event(
  summary="Project Review",
  start="2025-01-15T14:00:00-08:00",
  end="2025-01-15T15:00:00-08:00",
  description="Review Q1 progress"
)
```

## GitHub Projects

Query GitHub Projects (v2) for sprint planning and project management.

### Tools Overview

| Tool | Purpose |
|------|---------|
| `github_list_projects` | List all projects in org |
| `github_get_project` | Get project schema and fields |
| `github_project_items` | Query items with filters |

### Common Patterns

**List projects:**
```
github_list_projects()
# Returns project numbers and titles
```

**Get sprint backlog:**
```
github_project_items(project=1, status="Backlog")
```

**Get current sprint items:**
```
github_project_items(project=1, sprint="Sprint 65")
```

## Using Integrations in Reflexes

MCP tools (Notion, Calendar, GitHub) are called through the executive (Claude). For autonomous integration workflows, reflexes can wake the executive with relevant context:

```yaml
name: daily-sync-reminder
trigger:
  schedule: "0 9 * * *"
pipeline:
  - action: wake_executive
    message: "Morning sync: Check Notion project status and today's calendar"
```

For simpler triggers, reflexes can use internal Bud tools directly (GTD, journal, etc). See `state/notes/reflexes.md` for more on creating reflexes.
