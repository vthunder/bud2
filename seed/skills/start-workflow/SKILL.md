---
name: start-workflow
description: "Start a multi-step planning workflow. Invoke when user asks to 'plan X', 'create a plan for X', or explicitly names a workflow."
---

# start-workflow

Invoke when: user asks to "plan X", "create a plan for X", or explicitly names a workflow.

## Steps

1. Determine which workflow to start:
   - **Autopilot planning** ("plan X", "create a plan for X"): spawn `autopilot-vision:planner` directly (see below) — no workflow YAML needed
   - **Named workflow**: if the user names a specific workflow (e.g. "run plan-sprint"), use the workflow YAML approach below

2. **Autopilot planning entry point** (most common case):
   - Build context: "Plan this project: {user's description}\nProject path: {working directory or path from user}"
   - `Agent_spawn_async(task=<context>, agent="autopilot-vision:planner")`
   - `talk_to_user`: "Started autopilot planning — vision-planner is running. I'll cascade through strategy → epic → task once it completes. You'll hear from me when each level finishes or needs input."
   - The cascade from vision → strategy → epic → task happens automatically via `handle-subagent-complete` UP signals.

3. **Named workflow** (fallback for explicit workflow requests):
   - Generate a workflow instance ID: `wf_{unix_timestamp}`.
   - Create `state/system/workflow-instances/{id}.json`:
     ```json
     {
       "id": "wf_{timestamp}",
       "workflow_name": "{workflow_name}",
       "workflow_step": "{first_step_id}",
       "workflow_session_id": "",
       "input": "{user's project description}",
       "started_at": "{ISO8601}",
       "outputs": {}
     }
     ```
   - Load the workflow YAML from `state/system/workflows/{workflow_name}.yaml` to get the first step.
   - Render the first step's `context_template` with `{{.input}}` = the user's description.
   - `Agent_spawn_async` with the rendered context, `agent` = first step's agent name, `workflow_instance_id`, `workflow_step`.
   - `talk_to_user`: "Started {workflow_name} — running {first_step} agent. I'll update you when it completes."
