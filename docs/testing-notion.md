# Notion Integration Testing Guide

Manual test scenarios for verifying the Notion integration.

## Prerequisites

- `NOTION_API_KEY` set in environment
- Notion integration created at https://www.notion.so/my-integrations
- Integration added to at least one page/database in your workspace
- bud-mcp server running

## Setup

1. Create a Notion integration:
   ```
   https://www.notion.so/my-integrations → New integration
   ```

2. Copy the "Internal Integration Secret" (starts with `ntn_` or `secret_`)

3. Add integration to a Notion page:
   - Open a page in Notion
   - Click "..." menu → "Add connections" → Select your integration

4. Set env var:
   ```bash
   export NOTION_API_KEY="your-token-here"
   ```

5. Verify integration is enabled:
   ```bash
   BUD_STATE_PATH=state ./bin/bud-mcp 2>&1 | grep -i notion
   # Should see: "Notion integration enabled"
   ```

---

## Test 1: Search

**Goal:** Find pages and databases by text query

**Steps:**
1. Start a conversation with Bud
2. Ask: "Search Notion for [something in your workspace]"

**Expected:**
- Bud calls `notion_search` tool
- Returns list with id, type, title, url

**Direct tool test:**
```json
{
  "tool": "notion_search",
  "args": {
    "query": "Projects"
  }
}
```

**Expected output:**
```json
[
  {
    "id": "12345678-1234-...",
    "type": "database",
    "title": "Projects",
    "url": "https://notion.so/..."
  }
]
```

**Variations:**
- Filter by type: `"filter": "database"` or `"filter": "page"`
- Empty query returns recent items

---

## Test 2: Get Page

**Goal:** Retrieve page properties by ID

**Setup:** Get a page ID from search results or Notion URL

**Steps:**
1. Ask Bud: "Get the Notion page with ID [page-id]"

**Direct tool test:**
```json
{
  "tool": "notion_get_page",
  "args": {
    "page_id": "12345678-1234-1234-1234-123456789abc"
  }
}
```

**Expected output:**
```json
{
  "id": "12345678-...",
  "title": "My Page",
  "url": "https://notion.so/...",
  "properties": {
    "Name": "My Page",
    "Status": "In Progress",
    "Priority": "High"
  }
}
```

**Edge cases:**
- Invalid ID: Should return clear error
- Page without access: Should return 404/unauthorized error

---

## Test 3: Get Database Schema

**Goal:** Retrieve database structure including property types and options

**Setup:** Get a database ID from search results

**Steps:**
1. Ask Bud: "What's the schema for Notion database [id]?"
2. Or: "What columns/properties does [database name] have?"

**Direct tool test:**
```json
{
  "tool": "notion_get_database",
  "args": {
    "database_id": "12345678-1234-1234-1234-123456789abc"
  }
}
```

**Expected output:**
```json
{
  "id": "12345678-...",
  "title": "Projects",
  "url": "https://notion.so/...",
  "schema": {
    "Name": {"type": "title"},
    "Status": {
      "type": "status",
      "options": ["Not started", "In progress", "Done"]
    },
    "Priority": {
      "type": "select",
      "options": ["High", "Medium", "Low"]
    },
    "Due Date": {"type": "date"},
    "Assignee": {"type": "people"}
  }
}
```

**Verify:**
- Select/multi-select/status properties include options
- All property types are represented

---

## Test 4: Query Database

**Goal:** Query database with filters and sorting

**Setup:** Know a database ID and its schema

### 4.1 Basic Query (no filter)

```json
{
  "tool": "notion_query_database",
  "args": {
    "database_id": "12345678-..."
  }
}
```

**Expected:** Returns up to 50 pages with properties

### 4.2 Filter by Status

```json
{
  "tool": "notion_query_database",
  "args": {
    "database_id": "12345678-...",
    "filter": "{\"property\": \"Status\", \"status\": {\"equals\": \"In progress\"}}"
  }
}
```

### 4.3 Filter by Select

```json
{
  "tool": "notion_query_database",
  "args": {
    "database_id": "12345678-...",
    "filter": "{\"property\": \"Priority\", \"select\": {\"equals\": \"High\"}}"
  }
}
```

### 4.4 Filter by Text Contains

```json
{
  "tool": "notion_query_database",
  "args": {
    "database_id": "12345678-...",
    "filter": "{\"property\": \"Name\", \"title\": {\"contains\": \"urgent\"}}"
  }
}
```

### 4.5 Compound Filter (AND)

```json
{
  "tool": "notion_query_database",
  "args": {
    "database_id": "12345678-...",
    "filter": "{\"and\": [{\"property\": \"Status\", \"status\": {\"equals\": \"In progress\"}}, {\"property\": \"Priority\", \"select\": {\"equals\": \"High\"}}]}"
  }
}
```

### 4.6 With Sorting

```json
{
  "tool": "notion_query_database",
  "args": {
    "database_id": "12345678-...",
    "sort_property": "Due Date",
    "sort_direction": "ascending"
  }
}
```

**Expected output:**
```json
{
  "results": [
    {
      "id": "page-id-1",
      "title": "Task 1",
      "properties": {
        "Name": "Task 1",
        "Status": "In progress",
        "Priority": "High"
      }
    }
  ],
  "has_more": false,
  "count": 1
}
```

---

## Test 5: Natural Language Queries

Test that Bud can translate natural language to tool calls:

| User says | Expected tool call |
|-----------|-------------------|
| "Find my Projects database in Notion" | `notion_search(query="Projects", filter="database")` |
| "What's in my Projects database?" | `notion_get_database` then `notion_query_database` |
| "Show me high priority tasks" | `notion_query_database` with priority filter |
| "What tasks are in progress?" | `notion_query_database` with status filter |
| "Find anything about quarterly review" | `notion_search(query="quarterly review")` |

---

## Test 6: Error Handling

### 6.1 Invalid API Key

```bash
NOTION_API_KEY="invalid" ./bin/bud-mcp
```
**Expected:** Warning logged, tools not registered

### 6.2 Invalid Page/Database ID

```json
{
  "tool": "notion_get_page",
  "args": {"page_id": "not-a-real-id"}
}
```
**Expected:** Clear error message about invalid ID or not found

### 6.3 No Access to Page

Try to get a page the integration hasn't been added to.

**Expected:** Error about object not found or unauthorized

### 6.4 Invalid Filter JSON

```json
{
  "tool": "notion_query_database",
  "args": {
    "database_id": "...",
    "filter": "not valid json"
  }
}
```
**Expected:** "invalid filter JSON" error

### 6.5 Invalid Filter Property

```json
{
  "tool": "notion_query_database",
  "args": {
    "database_id": "...",
    "filter": "{\"property\": \"NonExistent\", \"status\": {\"equals\": \"Done\"}}"
  }
}
```
**Expected:** Notion API error about property not found

---

## Test 7: Integration with Bud Workflow

### 7.1 Bud Discovers Schema First

**Scenario:** User asks about a database Bud hasn't seen before

**Steps:**
1. Ask: "What high priority items are in my Tasks database?"

**Expected behavior:**
1. Bud searches for "Tasks database"
2. Gets database schema to understand properties
3. Queries with appropriate filter
4. Returns formatted results

### 7.2 Bud Remembers Database IDs

**Steps:**
1. Ask about a database
2. Later ask a follow-up question

**Expected:** Bud reuses the database ID from context without re-searching

---

## Troubleshooting

### "Notion integration disabled"
- Check `NOTION_API_KEY` is set
- Verify token starts with `ntn_` or `secret_`

### "object_not_found" errors
- Ensure integration is added to the page/database
- In Notion: Page menu → Add connections → Your integration

### Empty search results
- Integration only sees pages it's been added to
- Try adding integration to parent page (grants access to children)

### Properties not showing
- Some property types (formula, rollup) may not fully serialize
- Check database schema first to understand available properties

### Filter not working
- Property names are case-sensitive
- Use `notion_get_database` to see exact property names
- Status/select values must match exactly
