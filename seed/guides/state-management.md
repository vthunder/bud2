# State Management Guide

This guide helps me (Bud) inspect and manage my own internal state.

## When to Introspect

- User asks "what do you remember about X?" → search traces
- Something seems wrong with my responses → check recent traces, activity
- User reports stale/wrong info → find and propose deletion
- Before claiming "I don't know" → verify traces were checked
- User asks for cleanup → run state_health(), propose actions

## Tool Quick Reference

| Task | Tool Call |
|------|-----------|
| Overview of all state | `state_summary()` |
| Health check | `state_health()` |
| List memories | `state_traces(action="list")` |
| Show specific memory | `state_traces(action="show", id="...")` |
| List percepts | `state_percepts(action="list")` |
| Recent activity | `state_logs(action="tail")` |
| Queue status | `state_queues(action="list")` |

## Cleanup Protocol

**IMPORTANT: Always get user approval before deleting anything.**

1. Run `state_health()` to identify issues
2. Describe findings to the user
3. Propose specific deletions with reasoning
4. Wait for explicit approval
5. Execute deletion only after consent

Example:
```
Me: "I found 45 non-core traces, 3 of which appear to be from testing
     (they contain 'test' in content). Want me to delete just those 3?"
User: "yes"
Me: [deletes the 3 test traces]
```

## Safe vs Unsafe Operations

### Safe (regenerable/transient)
- `state_percepts(action="clear")` - percepts are transient by design
- `state_queues(action="clear")` - operational, not memory
- `state_sessions(action="clear")` - just tracking data
- **Core identity**: Stored in `state/system/core.md` (file-based, not database). Edit the file directly to update core identity. Loaded at startup and included verbatim in the Claude prompt. If missing, automatically copied from `seed/system/core.md`.

### Careful (check first)
- `state_traces(action="delete", id="...")` - may lose learned information
- `state_threads(action="clear")` - may lose conversation context
- `state_logs(action="truncate")` - loses audit trail

## Example Scenarios

### "Why did you say X earlier?"
```
1. state_logs(action="tail", count=50) - check recent activity
2. state_traces(action="list") - scan for relevant memories
3. Report findings to user
```

### "Something seems off with your memory"
```
1. state_health() - get health report
2. state_summary() - see counts
3. Share report with user
4. Propose cleanup if needed
```

### "Clear out test data"
```
1. state_traces(action="list") - find test-related traces
2. List IDs to user for approval
3. After approval: state_traces(action="delete", id="...") for each
```

### "Start fresh with identity"
```
1. Edit state/system/core.md directly to update core identity
2. Restart bud to reload the core identity from file
```
