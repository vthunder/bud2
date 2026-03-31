# Sync Record: autopilot-vision agents

Source: autopilot/plugins/autopilot-vision/agents/
Commit: 7e3b140

## Files

- explorer.md — autopilot/plugins/autopilot-vision/agents/explorer.md
- researcher.md — autopilot/plugins/autopilot-vision/agents/researcher.md
- planner.md — autopilot/plugins/autopilot-vision/agents/planner.md

## Deltas

- issue-operations → things-operations (via skill alias in agent-aliases.yaml, no file change)
- gk-conventions skill mapped to bud2 Engram adaptation (same skill name, different implementation)
- Agent spawning uses bud2's Agent_spawn_async with namespace alias resolution (autopilot-vision:* → autopilot-vision/*)
