# External Integrations

I can query and interact with external systems using MCP tools. Each integration has specific tools and patterns.

## General Pattern

1. **Search/List** - Find items by query or filter
2. **Get** - Retrieve details for a specific item
3. **Query** - Structured queries with filters and sorting
4. **Create/Update** - Modify data (where supported)

## Notion

Query my Notion workspace for pages, databases, and structured data.

### Tools

| Tool | Purpose |
|------|---------|
| `notion_search` | Find pages/databases by text |
| `notion_get_page` | Get page properties by ID |
| `notion_get_database` | Get database schema (columns, options) |
| `notion_query_database` | Query database with filters |

### Common Patterns

**Find something by name:**
```
notion_search(query="Project Alpha")
```

**Get database schema before querying:**
```
notion_get_database(database_id="abc123")
# Returns property types and select/status options
```

**Query with filters:**
```
notion_query_database(
  database_id="abc123",
  filter='{"property": "Status", "status": {"equals": "In Progress"}}'
)
```

**Filter examples:**
- Status equals: `{"property": "Status", "status": {"equals": "Done"}}`
- Text contains: `{"property": "Name", "title": {"contains": "urgent"}}`
- Checkbox: `{"property": "Archived", "checkbox": {"equals": false}}`
- Compound: `{"and": [filter1, filter2]}`

### Tips

- Use `notion_get_database` first to see available properties and valid values
- Database IDs look like UUIDs: `12345678-1234-1234-1234-123456789abc`
- Page IDs are similar but may have hyphens removed in URLs

## Google Calendar (Coming Soon)

Query and create calendar events.

### Planned Tools

| Tool | Purpose |
|------|---------|
| `calendar_list_events` | Get events in date range |
| `calendar_create_event` | Create new event |
| `calendar_free_busy` | Check availability |

## GitHub (Coming Soon)

Monitor repositories, PRs, and issues.

### Planned Tools

| Tool | Purpose |
|------|---------|
| `github_list_prs` | List open pull requests |
| `github_list_issues` | List issues |
| `github_get_notifications` | Get unread notifications |

## Using Integrations in Reflexes

Integrations can also be used in reflex pipelines for autonomous data pulling:

```yaml
name: daily-project-sync
trigger:
  schedule: "0 9 * * *"
pipeline:
  - action: notion_query_db
    database_id: "abc123"
    filter: {"property": "Status", "status": {"equals": "In Progress"}}
    output: projects
  - action: condition
    if: "len(projects.results) > 5"
    then:
      - action: wake_executive
        message: "{{len(projects.results)}} projects in progress"
```

See `state/notes/reflexes.md` for more on creating reflexes.
