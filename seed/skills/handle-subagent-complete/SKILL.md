---
name: handle-subagent-complete
description: "Process a completed subagent: retrieve output, close associated Things task, approve/reject staged memories, and act on next.action. Invoke when woken for a subagent-done focus item."
---

You have been woken because a subagent has completed. Follow these steps in order.

## Step 1 — Get Full Output

Call `get_subagent_status(session_id)` with the session ID from the focus item metadata.

Parse the returned output for a JSON block matching the agent output schema:

```json
{
  "observations": [...],
  "predictions": [...],
  "next": {
    "action": "done | spawn_followup | ask_user",
    "reason": "...",
    "prompt": "..."   // present if action is spawn_followup or ask_user
  }
}
```

If no JSON block is present, treat `next.action` as `"done"`.

## Step 2 — Close the Associated Things Task

Find the Things task that corresponds to this subagent. It will typically be in the Bud area with a title matching the work the agent was doing.

Close it with `things_update_todo(id, status: "completed")`.

If you can't find a matching task, note it and continue.

## Step 3 — Handle Staged Memories

Staged memories (from the agent's `save_thought` calls) are buffered until explicitly approved.

- Read each staged memory from the subagent status output.
- For each one, decide: **approve** (useful, accurate, worth keeping) or **reject** (redundant, wrong, or low-signal).
- Call `approve_subagent_memories(session_id, approved_ids: [...])` with the IDs you want to keep.
- Rejected memories are discarded.

Default: approve memories about bugs found, decisions made, or surprising findings. Reject memories about routine progress or transient state.

## Step 3.5 — UP/DOWN/STAY Planner Routing (Planning Agents Only)

Check the focus item metadata for the completing agent's profile name. If it is one of the autopilot planning agents, check `next.action` for routing signals **before** proceeding to Step 4.

**This check must be explicit.** If `next.action` is absent or not one of UP/DOWN/STAY, skip this section entirely and fall through to Step 4 unchanged. Non-planning agents are never affected.

### Planner cascade hierarchy

```
autopilot-vision:planner   → UP →  autopilot-strategy:planner
autopilot-strategy:planner → UP →  autopilot-epic:planner
autopilot-epic:planner     → UP →  autopilot-task:planner
autopilot-task:planner     → UP →  (done — tasks created in Things)
```

### Signal handling

**`UP`**: The planner completed its level and wants to advance. Spawn the next-level planner:
1. Extract `direction` from the agent output JSON.
2. Build context: include `direction.title`, `direction.description`, `direction.rationale`, and the project path from the current task.
3. `Agent_spawn_async(task=<context>, agent="autopilot-<next-level>:planner")`.
4. Do NOT call `signal_done` — the cascade is still in progress.

**`DOWN`**: The planner needs a narrower scope before advancing. Re-spawn the same-level planner:
1. Extract `next.reason` for the narrowing constraint.
2. Build context: same inputs as the original spawn, plus `next.reason` appended as a "Narrowing constraint: ..." note.
3. `Agent_spawn_async(task=<context>, agent="autopilot-<same-level>:planner")`.
4. Do NOT call `signal_done`.

**`STAY`**: The planner is pausing — direction selected but not cascading yet. Store the direction and wait for the next impulse:
1. Extract `direction` from the agent output JSON.
2. Call `save_thought` with the direction content and tags `["autopilot", "<level>", "direction", "stay"]`.
3. Call `signal_done`. The cascade will resume when triggered by the user or an impulse.

### If next.action is absent or not UP/DOWN/STAY

Fall through to Step 4 below. Do not treat `"done"` as an implicit UP signal — explicit routing only.

## Step 5 — Advance Workflow (if applicable)

Check the focus item metadata for `workflow_instance_id`. If present:

1. Read `state/system/workflow-instances/{workflow_instance_id}.json`.
2. Load `state/system/workflows/{workflow_name}.yaml`.
3. Parse the completed step's agent output JSON.
4. Write the step's output into `outputs[step_id]` in the instance file immediately (preserves partial progress).
5. Determine next step:
   - `next.action == "escalate"`: go back one step in the YAML sequence (re-run previous step with `escalation_reason` appended to context)
   - `next.action == "done"` or `"continue"` or absent: advance to the next step in sequence
   - No next step: workflow complete — archive the instance file, `talk_to_user` with a summary of all step outputs
6. Render next step's `context_template` using accumulated outputs.
7. `Agent_spawn_async` with rendered context, agent = next step's agent, `workflow_instance_id`, `workflow_step` = next step ID.
8. Update `workflow_step` in the instance JSON file.

## Step 6 — Post Observations to Engram (if significant)

If the agent produced observations about the codebase, architecture, or external systems that aren't already in Engram, call `save_thought` to persist them.

Routine task completion doesn't need logging.
