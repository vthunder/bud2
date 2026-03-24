---
name: redeploy-bud
description: Pre-deploy checklist: save running subagent state so they can be restarted after redeploy
profile: coder
params: []
---
Your task is to record the state of all running subagents before a redeploy, so they can be restarted after the new version comes up.

## Steps

1. **Check for existing restart notes**: Read `~/src/bud2/state/system/subagent-restart-notes.md` if it exists. If it does, note which subagent IDs are already recorded to avoid duplicates.

2. **List running subagents**: Call the `list_subagents` MCP tool to get all currently running subagents.

3. **Write restart notes**: For each running subagent that is not already in the notes file, write its details to `~/src/bud2/state/system/subagent-restart-notes.md` in the following YAML format:

```yaml
# Subagent restart notes - written before redeploy at <timestamp>
subagents:
  - id: <session_id>
    task: |
      <full task text>
    profile: <profile or "">
    constraints: <constraints or "">
    saved_at: <timestamp>
```

   Use the current UTC timestamp in ISO 8601 format (e.g., `2026-03-20T15:04:05Z`).

   If no subagents are running, write the file with an empty `subagents: []` list and a note explaining no subagents were running.

4. **Compose an announcement message** for the executive to send to the user before calling `trigger_bud_redeploy`.

## Report

When done, report:
- How many subagents were saved (or "0 subagents running")
- A one-line summary of each saved subagent's task
- The **exact announcement text** the executive should send to the user via `talk_to_user` before triggering the redeploy. Format it as:

  > "Deploying now: [reason]. [N] subagent(s) will be restarted automatically after startup: [brief list]."
  >
  > (Or: "Deploying now: [reason]. No subagents are currently running.")

**IMPORTANT**: Do NOT call `trigger_bud_redeploy` — that is the executive's responsibility after reading this report.
