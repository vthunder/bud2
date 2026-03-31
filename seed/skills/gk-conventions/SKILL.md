---
name: gk-conventions
description: >-
  Graph knowledge store conventions for autopilot planning agents. Teaches
  agents to read prior cycle data from Engram memory before planning, and to
  store directions, observations, and predictions after planning.
---

# GK Conventions (Bud2/Engram Adaptation)

> **Note:** In Bud2, the graph knowledge store (gk) is implemented by Engram memory. This skill replaces autopilot's `gk://` protocol with equivalent `search_memory` / `save_thought` patterns.

## Reading Prior Cycle Data

Before dispatching sub-agents or running /planning, always read prior cycle data. This prevents re-exploring known ground and surfaces prior decisions.

### Query pattern

Use `search_memory` with targeted queries:

```
search_memory("vision direction")        # prior vision cycles
search_memory("strategy direction")      # prior strategy cycles
search_memory("epic planning")           # prior epic cycles
search_memory("task decomposition")      # prior task cycles
search_memory("autopilot observations")  # cross-cycle observations
```

Read the results before acting. Prior directions inform what to explore and what diversity axes to use.

### What to look for

- **Directions**: Previously selected vision/strategy/epic/task directions
- **Rationale**: Why a prior direction was selected (what rubrics it scored on)
- **Observations**: Findings from prior explorer/researcher sub-agents
- **Predictions**: Prior predictions that can now be verified
- **Blockers**: Prior cycles that stalled or signaled DOWN/STAY

## Storing Results

After /planning completes, store results so the next cycle can build on them.

### Save directions

Use `save_thought` with structured content:

```
save_thought(
  content="Vision direction selected: <title>\nDescription: <description>\nRationale: <rationale>\nFitness: <score>",
  tags=["vision", "direction", "autopilot"]
)
```

Use the appropriate level tag: `vision`, `strategy`, `epic`, `task`.

### Save observations

Save significant findings from sub-agents:

```
save_thought(
  content="Explorer finding: <observation>\nSource: <file or market signal>\nConfidence: high/medium/low",
  tags=["observation", "autopilot", "<level>"]
)
```

### Save predictions

Save verifiable predictions for future cycles:

```
save_thought(
  content="Prediction: <what we expect to happen>\nBasis: <why we expect it>\nVerifiable by: <next cycle or timeframe>",
  tags=["prediction", "autopilot", "<level>"]
)
```

### Link hierarchy

When saving strategy, reference the parent vision direction. When saving epic, reference the parent strategy. This creates the hierarchy that future cycles read back.

```
save_thought(
  content="Strategy direction selected: <title>\nParent vision: <vision title>\nInvestment theme: <description>",
  tags=["strategy", "direction", "autopilot"]
)
```

## Validate Before Completing

After storing results, verify the save succeeded by running `search_memory` for the key you just saved. If it doesn't appear, save it again.

This replaces autopilot's `validate_graph` call.

## Important: No gk:// URLs

In Bud2, there are no `gk://guides/query` or `gk://guides/extraction` resources. Ignore those instructions in ported agent files — use `search_memory` and `save_thought` instead.
