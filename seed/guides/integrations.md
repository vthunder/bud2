# External Integrations

I can query and interact with external systems using MCP tools. Each integration has specific tools and patterns.

## General Pattern

1. **Search/List** - Find items by query or filter
2. **Get** - Retrieve details for a specific item
3. **Query** - Structured queries with filters and sorting
4. **Create/Update** - Modify data (where supported)

## Notion

Query and edit Notion pages and databases using the official `@notionhq/notion-mcp-server`.

### Setup

The official Notion MCP server is configured in `.mcp.json`:
```json
{
  "notion": {
    "command": "npx",
    "args": ["-y", "@notionhq/notion-mcp-server"],
    "env": { "NOTION_TOKEN": "${NOTION_API_KEY}" }
  }
}
```

Create an integration at https://notion.so/profile/integrations and share pages with it.

### Available Tools

The official MCP server provides ~21 tools. Key ones:

| Tool | Purpose |
|------|---------|
| `notion__search` | Search pages and databases |
| `notion__retrieve-a-page` | Get page properties |
| `notion__retrieve-a-page-content` | Get page content (markdown mode) |
| `notion__update-page-content` | Replace page content (markdown mode) |
| `notion__create-a-page` | Create new page |
| `notion__get-block-children` | Get blocks (JSON mode) |
| `notion__append-block-children` | Append blocks |
| `notion__query-a-database` | Query database with filters |
| `notion__retrieve-a-database` | Get database schema |
| `notion__create-a-comment` | Add comment to page |
| `notion__retrieve-comments` | Get comments |

### Markdown Mode (Recommended)

The official MCP has built-in markdown support - no manual conversion needed:

**Read page content:**
```
notion__retrieve-a-page-content(page_id="abc123")
# Returns markdown directly
```

**Update page content:**
```
notion__update-page-content(
  page_id="abc123",
  content="# New Title\n\nUpdated content here"
)
```

### Searching

```
notion__search(query="Project Alpha")
```

### Querying Databases

```
notion__query-a-database(
  database_id="abc123",
  filter={"property": "Status", "status": {"equals": "In Progress"}},
  sorts=[{"property": "Created", "direction": "descending"}]
)
```

**Filter examples:**
- Status: `{"property": "Status", "status": {"equals": "Done"}}`
- Text: `{"property": "Name", "title": {"contains": "urgent"}}`
- Checkbox: `{"property": "Archived", "checkbox": {"equals": false}}`
- Compound: `{"and": [filter1, filter2]}`

### Creating Pages

```
notion__create-a-page(
  parent_page_id="parent-id",
  title="New Page",
  content="# Content\n\n- Item 1\n- Item 2"
)
```

### Comments

```
notion__retrieve-comments(block_id="page-id")
notion__create-a-comment(
  page_id="page-id",
  content="My comment"
)
```

### Tips

- **Page IDs from URLs:** `notion.so/.../Page-Title-abc123` → use `abc123`
- **Use markdown mode** - `retrieve-a-page-content` and `update-page-content` are faster than working with JSON blocks
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
