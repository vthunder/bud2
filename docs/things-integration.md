# Things 3 Integration

Bud can integrate with Things 3 to use your existing GTD system instead of maintaining a separate JSON file.

## How It Works

**Read:** Direct SQLite queries to Things database (read-only mode, safe for cloud sync)
**Write:** Official `things:///` URL scheme (Things handles sync automatically)

This is the same architecture used by all successful Things integrations - it's safe and won't corrupt your data.

## Setup

### Prerequisites

1. Things 3 for Mac must be installed
2. Things must be running (for URL scheme writes to work)

### Enable Integration

Add to your `.env` file:

```bash
USE_THINGS=true

# Optional: Auth token for update operations
# Get from: Things → Settings → General → Enable Things URLs → Manage
THINGS_AUTH_TOKEN=your-auth-token-here
```

**About the auth token:**
- **Required for:** Updating existing tasks, projects, and areas
- **Not required for:** Creating new tasks (adding to inbox, projects)
- **Security:** Prevents malicious websites from modifying your Things data

Then rebuild and restart Bud:

```bash
cd /Users/thunder/src/bud2
go build -o bud ./cmd/bud
# Restart the bud service
```

### Verify

Check the logs on startup:

```bash
# Success:
[main] Using Things integration for GTD

# Fallback to JSON if Things not found:
Warning: failed to initialize Things store: Things database not found, falling back to JSON store
```

## Supported Operations

All GTD MCP tools work with Things:

- ✅ `gtd_list` - Read tasks, projects, areas
- ✅ `gtd_add` - Create new tasks via URL scheme
- ✅ `gtd_complete` - Mark tasks complete
- ✅ `gtd_update` - Update task details
- ✅ `gtd_areas` - Manage areas
- ✅ `gtd_projects` - Manage projects

## Limitations

1. **Writes are async** - URL scheme writes fire-and-forget, can't verify success immediately
2. **Things must be running** - URL scheme only works when Things app is open
3. **Rate limit** - 250 tasks per 10 seconds (shouldn't hit this in normal use)
4. **Some updates not supported** - Area updates via URL scheme aren't available

## Architecture

```
┌─────────────────────────────────────────────┐
│  Bud MCP Tools (gtd_add, gtd_list, etc.)   │
└─────────────────┬───────────────────────────┘
                  │
                  v
         ┌────────────────┐
         │ gtd.Store      │  <-- Interface
         │ interface      │
         └────────┬───────┘
                  │
         ┌────────┴────────┐
         │                 │
    ┌────v──────┐    ┌────v──────────┐
    │ GTDStore  │    │ ThingsStore   │
    │ (JSON)    │    │ (Things)      │
    └───────────┘    └───┬───────────┘
                         │
              ┌──────────┴──────────┐
              │                     │
         ┌────v────────┐       ┌───v────────────┐
         │ Things DB   │       │ things:/// URL │
         │ (read-only) │       │ scheme (write) │
         └─────────────┘       └────────────────┘
```

## Switching Back to JSON

Remove `USE_THINGS=true` from `.env` or set it to `false`, then restart Bud.

## Implementation Details

See:
- `internal/gtd/things_store.go` - Things backend implementation
- `internal/gtd/store.go` - JSON backend implementation
- `internal/gtd/types.go` - Store interface definition
- `cmd/import-things/main.go` - One-time migration tool (if needed)
