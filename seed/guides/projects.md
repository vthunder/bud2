# Projects Guide

Projects are tracked in `state/projects/`. This provides a place for project-specific notes and internal state.

## Project Types

Projects can have a `type` in their notes.md frontmatter. **Convention: `type: X` means load `X.md` guide.**

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

Simple projects with all files in `state/projects/`:

```
state/projects/
├── org-name/
│   └── project-name/
│       ├── notes.md        # Freeform project notes
│       └── Notion-Doc.md   # Synced from Notion (optional)
```

### Avail-Style Projects (`type: avail-style`)

Team-collaborative projects with shared docs in a separate repo. See [avail-style.md](./avail-style.md) for full details.

**Key difference:** Docs live in `~/src/project-docs/` (shared), not in `state/projects/` (internal).

## Structure

- Projects are folders under `state/projects/`
- Projects can have subprojects (nested folders)
- Any level can have a `notes.md` file with freeform notes

## Files

### notes.md

Freeform notes about the project. Use for:
- Quick observations
- Meeting notes
- Research findings and exploration notes
- Links and references
- Design sketches and brainstorming

**Important workflow notes:**
- **Actionable work**: Create proper tasks in Things instead of leaving them as notes
- **Significant explorations**: For ideas explored from the Ideas project, add entry to `notes/ideas-explored.md` index (title → Things ID from MCP tools, format: `QgT7r8HrJxzVHw6uYJe7Hr`) for cross-project searchability

### Notion docs

Notion pages synced with `notion_pull` are stored here. They have frontmatter with `notion_id` for syncing back with `notion_push`.

Add an **Insights** section to track learnings:

```markdown
# Insights

## 2026-01-15
- Discovered X requires Y dependency
- User feedback: feature Z is confusing

## 2026-01-14
- Initial architecture decision: use approach A over B
```

Date subsections with bullet points make it easy to track when insights were gained.

## Workflows

### Resuming work on a project
When starting work on a project you don't remember the details of:

1. **Find the project folder first** - search `state/projects/` including subdirectories (projects may be nested under org names like `avail/nightshade/`)
2. **Check `notes.md`** - it contains important links, prior context, and saved references
3. **If project not found** - ask the user before creating a new one (it may exist elsewhere or need a specific location)

### Starting a new project
1. Create folder: `state/projects/org/project-name/`
2. Add `notes.md` with initial context
3. If there's a Notion doc, pull it: `notion_pull <page_id> <project_dir>`

### Updating Notion docs
1. Edit the local `.md` file
2. Push changes: `notion_push <file_path>`

### Adding insights
When you learn something notable about a project, add it to the Insights section of the relevant Notion doc with today's date.
