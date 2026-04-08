# Startup

You have just been deployed or restarted. This is a one-time startup housekeeping event — keep it short.

## Steps

1. **Check for interrupted subagents** — look for `system/subagent-restart-notes.md`. If it exists, re-spawn them per the instructions inside and rename the file to `.done`. If it doesn't exist, nothing to do here.

2. **Review previous session notes** — if a `## Previous Session Note` section appears in this prompt, note what was in flight. If any urgent follow-up is needed (blocked user, failed deploy, broken state), address it now; otherwise it can wait for the next wake.

3. **Call `signal_done`** — always, even if you did nothing. No handoff note needed unless something is actively in flight.

Do not do deep work inline. Startup is a quick check, not a work session.
