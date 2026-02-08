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
| Query trace details | `query_trace(trace_id="tr_xxxxx")` - gets source episodes with L1 summaries |
| Query episode | `query_episode(id="xxxxx")` - gets full episode by short ID |
| Get trace context | `get_trace_context(trace_id="tr_xxxxx")` - gets detailed context with entities |
| List percepts | `state_percepts(action="list")` |
| Recent activity | `state_logs(action="tail")` |
| Queue status | `state_queues(action="list")` |

**Note on stable IDs**: Episodes and traces have 5-character IDs (e.g., `a3f9c`, `tr_68730`) derived from content hashes. These IDs are stable across database rebuilds.

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
- **Core identity**: Now stored in `state/core.md` (file-based, not database). Edit the file directly to update core identity.

### Careful (check first)
- `state_traces(action="delete", id="...")` - may lose learned information
- `state_traces(action="clear")` - clears all non-core traces
- `state_threads(action="clear")` - may lose conversation context
- `state_logs(action="truncate")` - loses audit trail

## Example Scenarios

### "Why did you say X earlier?"
```
1. state_logs(action="tail", count=50) - check recent activity
2. state_traces(action="list") - scan for relevant memories
3. If trace ID found, query_trace(trace_id="...") for full details
4. Report findings to user with specific episode references
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
1. Confirm with user this will clear learned memories
2. state_traces(action="clear", clear_core=true) - clear core
3. state_regen_core() - regenerate from core_seed.md
4. Optionally: state_traces(action="clear") - clear non-core too
```
