# Projects Guide

Projects are tracked in `state/projects/`. This provides a place for project-specific notes, metadata, and internal state.

## Project Structure

Each project has a folder under `state/projects/`:

```
state/projects/
├── bud/
│   ├── project.yaml    # metadata: name, repos, things_project_id, status
│   └── notes.md        # active context scratchpad
├── sandmill/
│   ├── project.yaml
│   ├── notes.md
│   └── guides/
│       └── vm-control.md   # project-specific guides live here
├── avail/
│   ├── nightshade/
│   │   ├── project.yaml
│   │   └── notes.md
│   └── docs/
│       ├── project.yaml
│       └── notes.md
└── ...
```

Sub-projects are supported (nested folders). Any level can have `project.yaml` and `notes.md`.

## project.yaml

Metadata for a project. Fields:

```yaml
name: Nightshade
repos:
  - availproject/nightshade     # GitHub slug or local path comment
  - nightshade-app              # local: ~/src/nightshade-app/
things_project_id: ""           # Things 3 project ID (empty if none)
status: active                  # active | paused | archived
description: One-line summary
```

## Project Types

Projects can have a `type` in their `notes.md` frontmatter. **Convention: `type: X` means load `X.md` guide.**

```yaml
---
type: avail-style       # → load avail-style.md guide
docs: ~/src/project-docs/nightshade/
---
```

When loading a project:
1. Read `notes.md` and check for `type:` in frontmatter
2. If present, load `state/system/guides/<type>.md`
3. Follow workflows in that guide

### Standard Projects (no type)

Simple projects with all files in `state/projects/`.

### Avail-Style Projects (`type: avail-style`)

Team-collaborative projects with shared docs in a separate repo. See [avail-style.md](./avail-style.md) for full details.

**Key difference:** Docs live in `~/src/project-docs/` (shared), not in `state/projects/` (internal).

## notes.md

Active context scratchpad. Use for:
- Current sprint focus and known gotchas
- Meeting notes and observations
- Research findings and exploration notes
- Links and references
- Design sketches and brainstorming

**Important workflow notes:**
- **Actionable work**: Create proper tasks in Things instead of leaving them as notes. Always include in the task notes: the project context and a link to the relevant file(s) in `state/projects/`.
- **Significant explorations**: For ideas explored from the Ideas project, add entry to `notes/ideas-explored.md` index (title → Things ID from MCP tools, format: `QgT7r8HrJxzVHw6uYJe7Hr`) for cross-project searchability. If the exploration produced a file, include the file path in the index entry.
- **Things notes policy**: All Things todos created from project work must have notes. At minimum: why the task was created + which project/file it relates to. On completion: what was done and where the output lives.

### Notion docs

Notion pages synced with `notion_pull` are stored here. They have frontmatter with `notion_id` for syncing back with `notion_push`.

Add an **Insights** section to track learnings:

```markdown
# Insights

## 2026-01-15
- Discovered X requires Y dependency
- User feedback: feature Z is confusing
```

## Before Working on Any Project

**Rule:** Before touching a repo or source file, use `list_projects` to find the relevant project and read its `notes.md`. If no project exists, offer to create one.

See also: [repositories.md](./repositories.md) — PR workflow, merge approval, branch conventions.

## Workflows

### Resuming work on a project

1. Use `list_projects` to find the project (searches all `project.yaml` files)
2. Read `notes.md` for current context, gotchas, and active decisions
3. Check for `type:` in frontmatter and load the relevant guide if present

### Starting a new project

Use the `create_project` MCP tool — it creates the folder, `project.yaml`, and `notes.md` in one call:
- `path`: relative path under `state/projects/` (e.g. `avail/myproject`)
- `name`: human-readable name
- `description`: one-line summary
- `repos`: comma-separated GitHub slugs (optional)
- `status`: active | paused | archived (default: active)

Then if there's a Notion doc, pull it: `notion_pull <page_id> <project_dir>`

### Project-specific guides

Place in `state/projects/<project>/guides/`. These take precedence over general guides for that project.

Example: `state/projects/sandmill/guides/vm-control.md` — Sandmill emulator control.

### Updating Notion docs

1. Edit the local `.md` file
2. Push changes: `notion_push <file_path>`

### Adding insights

When you learn something notable about a project, add it to the Insights section of the relevant Notion doc with today's date.
