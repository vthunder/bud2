---
name: start-workflow
description: "Start a multi-step planning workflow. Invoke when user asks to 'plan X', 'create a plan for X', or explicitly names a workflow."
---

# start-workflow

Invoke when: user asks to "plan X", "create a plan for X", or explicitly names a workflow.

## Steps

1. Determine which workflow YAML to use (`plan-project` for most planning requests).
2. Generate a workflow instance ID: `wf_{unix_timestamp}`.
3. Create `state/system/workflow-instances/{id}.json`:
   ```json
   {
     "id": "wf_{timestamp}",
     "workflow_name": "plan-project",
     "workflow_step": "{first_step_id}",
     "workflow_session_id": "",
     "input": "{user's project description}",
     "started_at": "{ISO8601}",
     "outputs": {}
   }
   ```
4. Load the workflow YAML from `state/system/workflows/{workflow_name}.yaml` to get the first step.
5. Render the first step's `context_template` with `{{.input}}` = the user's description.
6. `spawn_subagent` with the rendered context, `agent` = first step's agent name, `workflow_instance_id`, `workflow_step`.
7. `talk_to_user`: "Started {workflow_name} — running {first_step} agent. I'll update you when it completes."
