# Autonomous Features Testing Playbook

Manual test scenarios for verifying autonomous behavior in Bud.

## Prerequisites

- Bud running with Discord connected
- `AUTONOMOUS_ENABLED=true`
- `AUTONOMOUS_INTERVAL=5m` (shorter for testing)
- `DAILY_THINKING_BUDGET=30` (or higher)
- Access to Discord channel Bud monitors

---

## 1. Task System

### 1.1 Create a Task via MCP

**Setup:** Start a conversation with Bud

**Steps:**
1. Ask Bud: "Add a task to remind me to check the server logs in 2 minutes"
2. Wait for confirmation
3. Ask: "What tasks do I have?"

**Expected:**
- Bud calls `add_bud_task` tool
- Task appears in `list_bud_tasks` output
- Task has a due time ~2 minutes from now

**Verify:**
```bash
cat state/tasks.json | jq .
```

### 1.2 Task Becomes Due (Impulse Generation)

**Setup:** Task created with due time in the past or very soon

**Steps:**
1. Create overdue task: manually edit `state/tasks.json` to set `due` to past time
2. Restart Bud or wait for autonomous wake
3. Observe logs

**Expected:**
- Log shows impulse generated for overdue task
- Impulse has high intensity (>0.8)
- Bud may proactively mention the overdue task

**Verify in logs:**
```
[autonomous] Triggering wake-up via impulse
```

### 1.3 Complete a Task

**Steps:**
1. Ask Bud: "What are my tasks?"
2. Ask: "Mark the server logs task as done"

**Expected:**
- Bud calls `complete_bud_task` tool
- Task no longer appears in pending list

---

## 2. Ideas System

### 2.1 Add an Idea

**Steps:**
1. Ask Bud: "I have an idea - we should explore using WebSockets instead of polling"
2. Ask: "What ideas do you have saved?"

**Expected:**
- Bud calls `add_idea` tool
- Idea appears in list with `explored: false`

### 2.2 Idea Generates Impulse (Idle Only)

**Setup:** Bud is idle (no recent messages)

**Steps:**
1. Add several unexplored ideas
2. Wait for autonomous wake during idle period
3. Check logs

**Expected:**
- Ideas generate low-intensity impulses only during idle
- Bud might mention wanting to explore an idea

### 2.3 Mark Idea Explored

**Steps:**
1. Ask Bud to explore the WebSockets idea
2. After discussion, ask: "Mark that idea as explored"

**Expected:**
- Bud calls `explore_idea` with notes
- Idea marked as `explored: true`
- No longer generates impulses

---

## 3. Reflex System

### 3.1 Create a Reflex via MCP

**Steps:**
1. Ask Bud: "Create a reflex that responds to 'ping' with 'pong'"

**Expected:**
- Bud calls `create_reflex` tool
- File created at `state/reflexes/ping-pong.yaml`

**Verify:**
```bash
cat state/reflexes/*.yaml
```

### 3.2 Reflex Pattern Matching

**Setup:** Create a URL summarizer reflex manually:

```yaml
# state/reflexes/summarize-url.yaml
name: summarize-url
description: React to URLs with eyes emoji
trigger:
  pattern: "https?://\\S+"
  source: discord
pipeline:
  - action: react
    emoji: "eyes"
```

**Steps:**
1. Restart Bud to load reflexes
2. Post a message with a URL in Discord
3. Observe reaction

**Expected:**
- Reflex fires before executive
- Eyes emoji added to message
- Log shows: `[reflex] Fired: summarize-url`

### 3.3 Reflex with Template Action

**Setup:** Create greeting reflex:

```yaml
name: greeting
trigger:
  pattern: "^(gm|good morning)"
pipeline:
  - action: template
    output: msg
    params:
      template: "Good morning! Hope you have a great day."
  - action: reply
    message: "{{.msg}}"
```

**Steps:**
1. Restart Bud
2. Send "gm" in Discord

**Expected:**
- Reflex responds without waking executive
- Response appears quickly (<1s)

### 3.4 List and Delete Reflexes

**Steps:**
1. Ask Bud: "What reflexes do you have?"
2. Ask: "Delete the greeting reflex"

**Expected:**
- `list_reflexes` shows all loaded reflexes
- `delete_reflex` removes file and unloads

---

## 4. Autonomous Wake

### 4.1 Periodic Wake-Up

**Setup:**
- `AUTONOMOUS_ENABLED=true`
- `AUTONOMOUS_INTERVAL=2m`

**Steps:**
1. Start Bud
2. Don't send any messages
3. Wait 2+ minutes
4. Watch logs

**Expected:**
- Log: `[autonomous] Triggering wake-up via impulse`
- Impulse created with source `impulse:system`
- Executive receives percept and may take action

### 4.2 Budget Prevents Wake

**Setup:**
- `DAILY_THINKING_BUDGET=1`
- Exhaust budget by chatting

**Steps:**
1. Have a conversation to use up budget
2. Wait for autonomous interval
3. Check logs

**Expected:**
- Log: `[autonomous] Skipping wake-up: <reason>`
- No executive invocation

### 4.3 signal_done Tracking

**Steps:**
1. Send Bud a message
2. Wait for response
3. Check if `signal_done` was called

**Expected:**
- Bud calls `signal_done` at end of response
- Session tracker records completion
- CPU monitor sees idle state

**Verify:**
```bash
tail -f state/signals.jsonl
```

---

## 5. Journal System

### 5.1 Journal Logging

**Steps:**
1. Ask Bud to do something (e.g., "What's the weather concept for our app?")
2. Ask: "What did you do today?"

**Expected:**
- Bud calls `journal_log` during work
- `journal_today` shows entries

**Verify:**
```bash
tail -20 state/journal.jsonl | jq .
```

### 5.2 Journal Entry Types

**Steps:**
1. Trigger different entry types through conversation:
   - Decision: "Should we use Redis or Postgres?"
   - Action: "Create a new file called test.txt"
   - Observation: "I noticed the tests are slow"

**Expected:**
- Journal entries have appropriate `type` field
- Each has `summary`, optional `context`, `reasoning`, `outcome`

---

## 6. Memory Traces

### 6.1 Save a Thought

**Steps:**
1. Tell Bud: "Remember that I prefer morning standup at 9am"
2. Ask: "List your memory traces"

**Expected:**
- Bud calls `save_thought`
- Trace appears in `list_traces`

### 6.2 Mark Trace as Core

**Steps:**
1. List traces to get ID
2. Ask Bud: "Mark that preference as core to your identity"

**Expected:**
- Trace marked with `is_core: true`
- Will be included in all future prompts

### 6.3 Create Core Directly

**Steps:**
1. Ask: "Add to your core identity that you're helping build an autonomous agent"

**Expected:**
- Bud calls `create_core`
- New trace created with `is_core: true`

---

## 7. Integration Scenarios

### 7.1 Task Due + Autonomous Wake

**Scenario:** Task becomes due, triggers proactive notification

**Steps:**
1. Create task due in 3 minutes
2. Set `AUTONOMOUS_INTERVAL=1m`
3. Wait without messaging
4. Observe

**Expected:**
- Autonomous wake sees overdue task
- Bud proactively messages about it

### 7.2 Reflex + Executive Escalation

**Scenario:** Reflex handles simple case, complex case goes to executive

**Setup:** Create reflex that only handles exact pattern

**Steps:**
1. Send message matching reflex pattern exactly
2. Send similar but not exact message

**Expected:**
- Exact match: reflex handles, no executive
- Near match: falls through to executive

### 7.3 Full Autonomous Cycle

**Scenario:** Bud wakes, checks tasks/ideas, does background work

**Steps:**
1. Add several tasks (none due) and ideas
2. Set short autonomous interval
3. Leave Bud idle for several cycles
4. Check journal

**Expected:**
- Multiple wake-ups logged
- Bud may explore ideas during idle
- Journal shows autonomous activity

---

## Troubleshooting

### Reflex Not Firing
- Check pattern regex is valid
- Verify source filter matches (discord vs github)
- Check logs for `[reflex] Loaded` on startup

### Autonomous Wake Not Happening
- Verify `AUTONOMOUS_ENABLED=true`
- Check budget isn't exhausted
- Look for `[autonomous] Skipping` in logs

### MCP Tools Not Working
- Verify bud-mcp is running
- Check Claude can see tools in conversation
- Look for errors in MCP server logs

### Tasks/Ideas Not Persisting
- Check `state/tasks.json` and `state/ideas.json` exist
- Verify write permissions
- Look for JSON parse errors in logs
