# External Integrations

I can query and interact with external systems using MCP tools. Each integration has specific tools and patterns.

## General Pattern

1. **Search/List** - Find items by query or filter
2. **Get** - Retrieve details for a specific item
3. **Query** - Structured queries with filters and sorting
4. **Create/Update** - Modify data (where supported)

## Notion

Query and edit Notion pages, databases, and blocks using the official Notion MCP server.

### Architecture

**Official Notion MCP Tools** (`mcp__notion__API-*`) handle all HTTP operations.
**Bud Conversion Tools** (`notion_*`) convert between markdown and Notion's block format.

Workflow:
1. Use official API tools for all HTTP operations (search, get, update)
2. Use Bud tools to convert markdown ↔ JSON blocks

### Official API Tools

| Tool | Purpose |
|------|---------|
| `API-post-search` | Search pages and databases by text |
| `API-retrieve-a-page` | Get page properties |
| `API-patch-page` | Update page properties |
| `API-post-page` | Create new page |
| `API-get-block-children` | Get blocks on a page |
| `API-patch-block-children` | Append blocks to page |
| `API-retrieve-a-block` | Get single block |
| `API-update-a-block` | Update block content |
| `API-delete-a-block` | Delete a block |
| `API-query-data-source` | Query database with filters |
| `API-retrieve-a-data-source` | Get database schema |
| `API-move-page` | Move page to new parent |
| `API-create-a-comment` | Add comment to page |
| `API-retrieve-a-comment` | Get comments on page |

### Bud Sync Tools (Recommended)

Efficient bidirectional sync between markdown files and Notion pages. Uses direct API calls - much faster than manual block-by-block operations.

| Tool | Purpose |
|------|---------|
| `notion_pull` | Fetch page → local markdown file (with comments as blockquotes) |
| `notion_diff` | Compare local file vs Notion page |
| `notion_push` | Push local file → Notion (erase + replace, 2 API calls total) |

**Setup:** Reads `NOTION_API_KEY` from environment or `.env` file automatically.

**Workflow:**
```
# Pull a page to edit locally
notion_pull(page_id="abc123")
# → Creates /tmp/notion/PageTitle.md with frontmatter

# Edit the markdown file (Claude is efficient at this!)

# Check what would change
notion_diff(file_path="/tmp/notion/PageTitle.md")

# Push changes back
notion_push(file_path="/tmp/notion/PageTitle.md")
```

**File format:**
```markdown
---
notion_id: abc123
title: Page Title
pulled_at: 2025-01-14T10:30:00Z
---

# Content here

---

## Comments

> 💬 **username** *(Jan 14)*: Original comment preserved as blockquote
```

### Bud Conversion Tools

Lower-level conversion utilities - use when you need fine-grained control:

| Tool | Input | Output |
|------|-------|--------|
| `notion_get_content` | JSON blocks array | Markdown string |
| `notion_create_page` | Markdown string | JSON blocks array |
| `notion_insert_block` | Block type + markdown | Single JSON block |
| `notion_update_block` | Markdown string | rich_text JSON array |
| `notion_list_blocks` | JSON blocks array | Simplified ID/type list |

### Common Workflows

**Read page content as markdown:**
```
1. API-get-block-children(block_id="page-id")  → JSON blocks
2. notion_get_content(blocks=<JSON from step 1>)  → markdown
```

**Write markdown to page:**
```
1. notion_create_page(markdown="# Title\n\n- Item 1")  → JSON blocks
2. API-patch-block-children(block_id="page-id", children=<JSON from step 1>)
```

**Replace page content (use sync tools instead!):**
```
# Recommended: Use notion_pull → edit → notion_push

# Manual approach (slow, many API calls):
1. API-get-block-children(block_id="page-id")  → get block IDs
2. For each block: API-delete-a-block(block_id=...)
3. notion_create_page(markdown=<new content>)  → JSON blocks
4. API-patch-block-children(block_id="page-id", children=<JSON from step 3>)
```

### Searching

```python
API-post-search(query="Project Alpha")
```

### Querying Databases

```python
API-query-data-source(
  data_source_id="abc123",
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

```python
# Step 1: Convert markdown to blocks
notion_create_page(markdown="## Section\n\n- Item 1\n- Item 2")
# Returns: [{"type": "heading_2", ...}, {"type": "bulleted_list_item", ...}]

# Step 2: Create page with blocks
API-post-page(
  parent={"page_id": "parent-id"},
  properties={"title": [{"text": {"content": "New Page"}}]},
  children=<blocks from step 1>
)
```

### Block Format

Notion API block structure:
```json
{"type": "paragraph", "paragraph": {"rich_text": [{"text": {"content": "Text"}}]}}
{"type": "heading_1", "heading_1": {"rich_text": [{"text": {"content": "Title"}}]}}
{"type": "bulleted_list_item", "bulleted_list_item": {"rich_text": [{"text": {"content": "Item"}}]}}
```

Supported block types: `paragraph`, `heading_1`, `heading_2`, `heading_3`, `bulleted_list_item`, `numbered_list_item`, `to_do`, `quote`, `divider`, `table`

### Markdown Formatting

Bud tools support inline markdown: `**bold**`, `*italic*`, `` `code` ``, `[link](url)`

### Comments

```python
API-retrieve-a-comment(block_id="page-id")
API-create-a-comment(
  parent={"page_id": "page-id"},
  rich_text=[{"text": {"content": "My comment"}}]
)
```

### Moving Pages

```python
API-move-page(
  page_id="page-to-move",
  parent={"type": "page_id", "page_id": "new-parent-id"}
)
```

### Tips

- **Page IDs from URLs:** `notion.so/.../Page-Title-abc123` → `abc123`
- **Pagination:** Official tools return `has_more` and `next_cursor` for large results
- **100 block limit:** API-patch-block-children accepts max 100 blocks per call

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
