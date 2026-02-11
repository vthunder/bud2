# Executive Text Capture System

## Overview

The executive's text capture system serves as a **fallback mechanism** to ensure user messages always get a response, even when Claude fails to use the proper MCP communication tools (`talk_to_user`, `discord_react`).

## How Claude Sessions Work

When Claude runs in `--print --output-format stream-json` mode, it produces a stream of events:

### Event Types

**Per-turn events** (can be many):
- `assistant` - Generated each time Claude produces output (text or tool calls)
- `user` - Tool results fed back to Claude
- `tool_use` - Individual tool call events

**Final event** (always one):
- `result` - Summary of the entire session (may or may not contain text)

### Example Session Flow

```
Turn 1:
  - system event (turn start)
  - assistant event → calls talk_to_user("Hey!")
  - user event (tool result)

Turn 2:
  - assistant event → generates internal analysis text (534 chars)

Turn 3:
  - assistant event → calls signal_done
  - user event (tool result)

Final:
  - result event (may be empty or contain summary)
```

## Text Capture Strategy

### Purpose

The `onOutput` callback captures text from assistant and result events to serve **two purposes**:

1. **Primary**: Fallback message when Claude forgets to call `talk_to_user`
2. **Secondary**: Extract `memory_eval` JSON and provide debugging logs

### Implementation

```go
// In executive_v2.go
var output strings.Builder
e.session.OnOutput(func(text string) {
    output.WriteString(text)
})
```

The captured text flows into `simple_session.go`:

```go
case "assistant":
    // Capture text from assistant event
    if s.currentPromptHasText {
        return // Anti-duplication: skip if already captured
    }
    // ... extract text and call s.onOutput(text)
    s.currentPromptHasText = true

case "result":
    // Capture text from result event
    if s.currentPromptHasText {
        return // Anti-duplication: skip if already captured from assistant
    }
    // ... extract text and call s.onOutput(text)
    s.currentPromptHasText = true
```

### Anti-Duplication Guard

The `currentPromptHasText` flag prevents capturing text twice:

- **Both events have text**: Capture from first event (usually assistant), skip second
- **Only one has text**: Capture from whichever has it
- **Neither has text**: `output.String()` remains empty

This is critical because:
- Text-only responses: Both assistant AND result may contain the same text
- Tool-based responses: Usually only assistant has text, result is empty
- We never want to send duplicate fallback messages

## Usage Scenarios

### Scenario 1: ✅ Claude Correctly Uses MCP Tools

```
User: "hi"
Claude Turn 1: calls talk_to_user("Hey! 👋")
Claude Turn 2: generates text "Looking at the bug pattern..."
Claude Turn 3: calls signal_done
```

**Flow**:
- `talk_to_user` sends message to user ✓
- Text from Turn 2 captured in `output`
- Validation: `mcpToolCalled["talk_to_user"] = true` → PASS
- Fallback: NOT triggered
- Output text used only for: logging, memory_eval extraction

**Result**: User sees "Hey! 👋", internal text logged but not sent

### Scenario 2: ❌ Claude Forgets to Call MCP Tools

```
User: "hi"
Claude: generates text "Hello! How can I help?"
        (but forgets to call talk_to_user)
```

**Flow**:
- Text captured in `output` from assistant/result event
- Validation: `mcpToolCalled["talk_to_user"] = false` → FAIL
- Fallback: TRIGGERED
- `output.String()` sent as fallback message

**Sub-scenarios**:
- **2.1**: Result event has text → use it (preferred, it's the summary)
- **2.2**: Only assistant has text → use it
- **2.3**: No text anywhere → send generic error: `"[Internal error: response was generated but not sent. This is a bug.]"`

**Result**: User sees the generated text (or error message)

### Scenario 3: ✅ Autonomous Wake (Not User Message)

```
Autonomous wake
Claude: does background work, may or may not communicate
```

**Flow**:
- `isUserMessage = false`
- Validation: NOT required (no talk_to_user needed)
- Fallback: NOT triggered
- Output used only for logging

**Result**: No user communication required or enforced

## Validation Logic

From `executive_v2.go`:

```go
// Check if this was a user message that requires response
mcpResponseSent := e.mcpToolCalled["talk_to_user"] || e.mcpToolCalled["discord_react"]
isUserMessage := item.Priority == focus.P1UserInput || item.Source == "discord" || item.Source == "inbox"

if isUserMessage && !userGotResponse && !mcpResponseSent {
    // VALIDATION FAILED - Claude forgot to communicate

    // Build fallback message from captured text
    fallbackMsg := strings.TrimSpace(output.String())
    if fallbackMsg == "" {
        fallbackMsg = "[Internal error: response was generated but not sent. This is a bug.]"
    }

    // Send via fallback (bypasses MCP)
    e.config.SendMessageFallback(channelID, fallbackMsg)
}
```

## Why This Design?

### Problem
Claude sometimes generates a thoughtful response but forgets to call the `talk_to_user` MCP tool. This results in:
- User sees nothing (bad UX)
- Claude's work is wasted
- Conversation thread breaks

### Solution
Capture all text output as a safety net. If Claude uses tools correctly, ignore it (just use for logging). If Claude forgets, send the captured text as a fallback.

### Design Principles

1. **MCP tools are the primary path** - We want Claude to use `talk_to_user` explicitly
2. **Text capture is the fallback** - Only used when tools fail
3. **Anti-duplication is critical** - Never send duplicate messages
4. **Graceful degradation** - Always give user *something* rather than silence

## Historical Context

### Commit ff909c3 (Feb 9, 2026)
"fix: eliminate duplicate text output in Claude responses"

Disabled assistant event processing entirely:
```go
case "assistant":
    // Skip - text output is handled by "result" event
```

**Problem**: This broke tool-based responses where result event is empty.

### Current Fix (Feb 11, 2026)
Re-enabled assistant event processing WITH anti-duplication guard:
```go
case "assistant":
    if s.currentPromptHasText { return }
    // ... process text

case "result":
    if s.currentPromptHasText { return }
    // ... process text
```

**Result**: Captures text from either event (whichever has it), prevents duplicates.

## Testing

To verify correct behavior:

```bash
# Build and run test
go build -o bud-test cmd/bud/main.go
./bud-test > test.log 2>&1 &

# Send test message via Discord
# Then check logs:

# Should see exactly ONE talk_to_user call per user message:
grep "\[mcp\] talk_to_user" test.log | wc -l

# Should see text captured but not duplicated:
grep "Assistant event text\|Result event text" test.log

# Check validation:
grep "ERROR: User message completed without response" test.log
```

## Related Files

- `internal/executive/simple_session.go` - Event handling and text capture
- `internal/executive/executive_v2.go` - Validation and fallback logic
- `internal/executive/types.go` - StreamEvent structure
- `state/system/core.md` - Claude's instructions about using talk_to_user

## Future Improvements

Potential areas for enhancement:

1. **Detect when result duplicates assistant** - Could compare text similarity and skip result if identical
2. **Tool call extraction** - Parse tool calls from assistant event to provide better error messages
3. **Streaming output** - Currently buffers all text; could stream for long responses
4. **Multi-turn analysis** - Track which turn generated which text for better debugging
