# Sync Record: autopilot-task agents

Source: autopilot/plugins/autopilot-task/agents/
Commit: 7e3b140

## Files

- explorer.md — autopilot/plugins/autopilot-task/agents/explorer.md
- decomposer.md — autopilot/plugins/autopilot-task/agents/decomposer.md
- planner.md — autopilot/plugins/autopilot-task/agents/planner.md

## Deltas

- issue-operations → things-operations (via skill alias in agent-aliases.yaml, no file change)
- gk-conventions skill mapped to bud2 Engram adaptation (same skill name, different implementation)
- Agent spawning uses bud2's Agent_spawn_async with namespace alias resolution (autopilot-task:* → autopilot-task/*)
