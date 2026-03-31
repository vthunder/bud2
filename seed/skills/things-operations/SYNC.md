# Sync Record: things-operations

Source: autopilot/plugins/issues-linear/skills/issue-operations/SKILL.md
Commit: 7e3b140

## Deltas

- Backend swapped: Linear MCP → Things 3 MCP (`things_*` tools)
- Epics map to Things **projects** (not Linear parent issues)
- Tasks map to Things **todos** within those projects
- Issue relations map to **notes annotations + creation ordering** (Things has no native blocking relations)
- Claiming = `when=today` (not assign + state change)
- Status updates use `completed=True` and `status:blocked` tag (not state machine transitions)
- Default area: `Bud` (read from `bud.yaml` as `planning_area`)
- Hard error if no area configured — no silent writes to wrong area
- `autopilot:managed` tag convention preserved
