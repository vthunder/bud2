---
name: startup
description: Post-startup housekeeping: re-spawn any subagents that were interrupted by a redeploy
profile: coder
params: []
---
Your task is to check for interrupted subagents from the previous session and re-spawn them.

## Steps

1. **Check for restart notes**: Look for the file `~/src/bud2/state/system/subagent-restart-notes.md`.

2. **If the file does NOT exist**: Report "No restart notes found - clean startup" and stop. No further action needed.

3. **If the file exists**:
   a. Read the file and parse the list of subagents to restart.
   b. For each subagent entry, call `Agent_spawn_async` with the saved `task`, `profile`, and `constraints` fields.
   c. After all subagents have been spawned, rename the file to `~/src/bud2/state/system/subagent-restart-notes.md.done` (append `.done` suffix) so it won't be processed again on the next startup.

## Report

When done, report:
- Whether restart notes were found
- How many subagents were re-spawned
- The task summary for each re-spawned subagent
- Any errors encountered during spawn

If no restart notes were found, simply report: "No restart notes found - clean startup."
