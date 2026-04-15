# GTD System

All task management — both Bud's own tasks and the owner's personal GTD — lives in Things 3. The old `gtd_*` MCP tools have been removed. Use `things_*` tools exclusively.

## Bud's Tasks (Things "Bud" area)

Bud's own commitments and work items live in the **Bud** area (area ID: `K2v9QVdae4jBuTy9VSDvYc`).

```
things_get_area id="K2v9QVdae4jBuTy9VSDvYc"   # list Bud's tasks
things_add_todo title="..." list="Bud"           # add to Bud area
things_update_todo id="..." ...                  # update a task
```

Ideas live in the **Ideas** project under the Bud area (someday). Explored ideas are indexed at `notes/ideas-explored.md`.

## Owner's Personal GTD

The owner's Work, Life, Health, and other areas are also in Things 3. Use `things_*` tools to read and manage them.

```
things_get_today                                 # today's tasks across all areas
things_get_inbox                                 # inbox items
things_get_areas                                 # list all areas
things_get_projects area_id="..."                # projects in an area
things_add_todo title="..." list="Inbox"         # quick-capture to inbox
things_update_todo id="..." when="today"         # move to today
```

See `things-mcp.md` for the full tool reference.

## Note on Removed Tools

The `gtd_add`, `gtd_list`, `gtd_update`, `gtd_complete`, `gtd_areas`, and `gtd_projects` tools have been removed. They were backed by a `user_tasks.json` flat file that no longer exists. All tasks have been migrated to Things 3.
