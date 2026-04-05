---
topic: Session Lifecycle & Context Assembly
repo: bud2
generated_at: 2026-04-06T00:20:00Z
commit: c7aa9287
key_modules: [internal/executive, internal/types, internal/memory]
score: 1.00
---

# Session Lifecycle & Context Assembly

> Repo: `bud2` | Generated: 2026-04-06 | Commit: `c7aa9287`

## Summary

This subsystem governs the full arc of a Bud wake — from an inbox item arriving to Claude responding and the session persisting to disk for the next wake. It is the most-changed part of the codebase (112 commits/90d) because it sits at the intersection of focus, memory, Claude SDK, and I/O. Every user message, autonomous wake, impulse, and subagent completion flows through this path.

## Key Data Structures

### `SimpleSession` (`internal/executive/simple_session.go`)
The stateful envelope around a single long-lived Claude conversation. Holds two distinct session IDs:
- `sessionID` — a fresh UUID generated each turn (Bud's internal tracking key, never sent to Claude)
- `claudeSessionID` — the session ID assigned by Claude's SDK on the first `StreamEvent` of a turn; persisted to disk and used for `--resume` on the next wake

Also tracks: `seenItemIDs`, `seenMemoryIDs`, `lastBufferSync`, `userMessageCount`, `lastUsage`, and `isResuming`. All of these are per-session state that controls what context gets injected on each turn.

### `ExecutiveV2` (`internal/executive/executive_v2.go`)
The main orchestrator. Owns the `SimpleSession`, focus `attention` and `queue`, `engram.Client` for memory, `SubagentManager`, and all configuration callbacks. Exposes `ProcessNextP1` (user-priority) and `ProcessNextBackground` (autonomous), which run on separate goroutines allowing true concurrency between user responses and background work.

### `ExecutiveV2Config` (`internal/executive/executive_v2.go`)
Configuration injected at startup: model, working dir, MCP server URL, typing indicator callbacks, `OnExecWake`/`OnExecDone` hooks, `MaxAutonomousSessionDuration`, and `WakeupInstructions`. The `WakeupInstructions` field is loaded from `seed/wakeup.md` and injected only into autonomous wake prompts, not user-message prompts.

### `InboxMessage` (`internal/memory/inbox.go`)
The raw input envelope from the intake pipeline. Three types:
- `message` — external input (Discord, synthetic). Converted to a `Percept` and stored in memory.
- `signal` — control signals (e.g. `signal:done`). Not converted to percepts; processed directly by the executive.
- `impulse` — internal motivations (task due, scheduled wake). Converted to a percept for unified attention scoring.

### `Thread` (`internal/types/types.go`)
A conversation thread with a dual status model: `ThreadStatus` (logical: `active`, `paused`, `complete`) and `SessionState` (runtime: `focused`, `active`, `frozen`, `""`). The session state tracks whether a Claude process exists for this thread. On Bud restart, `ThreadPool.Load()` resets any `active` threads to `paused` because no Claude process survived the restart.

### `execSessionDiskFormat` (`internal/executive/simple_session.go`)
The on-disk record that bridges restarts. Written to `state/system/executive-session.json` after every completed wake. Contains `claude_session_id`, `saved_at`, and `cache_read_tokens`. Loaded at startup so the next wake can resume the same Claude session rather than starting cold.

## Lifecycle

1. **Intake**: An `InboxMessage` arrives in `internal/memory/inbox.go`. Message-type items are converted to `Percept` via `ToPercept()` — this computes a `conversation_id` from the channel + 5-minute time bucket, assigns intensity, and copies source metadata into `Data`.

2. **Enqueue**: The percept (or raw impulse for system-type items) is added to the focus queue via `ExecutiveV2.AddPending()`. The focus module scores salience and assigns a priority tier (`P0`=emergency, `P1`=user input, `P2`=subagent questions, `P3`=subagent done, `P4`=autonomous wakes).

3. **Dispatch**: The main processing loop calls `ProcessNextP1` or `ProcessNextBackground` based on priority. Both call `processItem()`, but the P1 path first calls `RequestBackgroundInterrupt()` to cancel any in-flight background session.

4. **Resume decision** (`processItem`, line ~2808): Checks `session.ClaudeSessionID() != ""` and `!session.ShouldReset()`. If both hold, calls `PrepareForResume()` — which preserves `claudeSessionID`, `seenMemoryIDs`, and `lastBufferSync` but generates a fresh `sessionID` and clears `memoryIDMap`. Otherwise calls `PrepareNewSession()` which rotates the session entirely (new UUID, cleared state, `claudeSessionID = ""`). **Important**: `ShouldReset()` is evaluated before `PrepareNewSession` is called — the previous turn's token usage drives the reset decision.

5. **Context assembly** (`buildContext()`): Assembles a `focus.ContextBundle` containing:
   - Core identity text (cached from `state/system/core.md`)
   - Recent conversation episodes (last 20, within 10 min window, bot responses filtered on resume turns)
   - Activated memories from Engram (spread activation against current percept embedding)
   - Reflex log entries from recent automated handling
   - Any subagent pending questions (injected as `SubagentQuestions` for Claude to relay)
   - The current focus item (`PendingItem`) — the percept content, priority, source, and metadata

6. **Prompt build** (`buildPrompt()`): Renders the bundle into a text prompt. When `isResuming == true`, skips static sections (core identity, full conversation buffer) that are already in Claude's context window — only includes new memory traces not yet seen this session and new buffer episodes since `lastBufferSync`. For autonomous wake items, injects `WakeupInstructions` and wake-specific context.

7. **Send to Claude** (`SimpleSession.SendPrompt()`):
   - Opens/appends to `logs/exec/executive.log` (single persistent log, all wakes append)
   - Builds SDK client with `WithMcpServers(mcpURL)`, `WithPartialStreaming()`
   - If `claudeSessionID != ""`, adds `WithResume(claudeSessionID)` so Claude resumes its session
   - Wraps context with `signal_done` cancel + optional `MaxAutonomousSessionDuration` timeout
   - Calls `receiveLoop()` which dispatches `StreamEvent` → `TextBlock` → `ToolUseBlock` → `ResultMessage`

8. **Session ID capture**: The first `StreamEvent` that carries a non-empty `SessionID` is used to set `claudeSessionID`. This happens within milliseconds of session start. Using `StreamEvent` rather than `ResultMessage` is critical because `signal_done` can cancel the context before `ResultMessage` arrives, which would lose the session ID.

9. **signal_done fires**: `SignalDone()` calls `sessionCancel()` on the current session's context. `SendPrompt` returns with `ErrInterrupted`. The executive treats this as a normal completion and falls through to post-completion bookkeeping.

10. **Post-completion**:
    - `SaveSessionToDisk()` — writes `claudeSessionID` to `executive-session.json`
    - `MarkMemoriesSeen(memoryIDs)` — prevents re-injecting memories on the next resume turn
    - `ResolveMemoryEval()` — maps the `{"a3f9c": 5}` ratings from Claude's output back to full trace IDs via `memoryIDMap`; calls `OnMemoryEval` callback which writes ratings to Engram
    - `OnExecDone` fires with `focusID`, summary, duration, and `SessionUsage`

11. **Graceful recovery**: If `WithResume` references a session that no longer exists (e.g. Claude's state was purged), `isSessionNotFoundError()` detects the error, clears `claudeSessionID`, and retries the same prompt as a fresh session. This prevents a stale on-disk session ID from causing permanent wake failures.

## Design Decisions

- **Single persistent session**: `ExecutiveV2` uses one `SimpleSession` across all wakes rather than per-thread sessions. The comment in the struct says "Key simplification." This trades per-conversation isolation for simpler state management and lower overhead — each wake resumes the same Claude conversation unless context overflows.

- **Two-tier token reset**: `ShouldReset()` checks `cache_read_input_tokens + input_tokens > 150K` (not output tokens or message count). Cache read tokens tell you how much prior session history Claude loaded from its KV cache — the real measure of context pressure. The 150K threshold leaves ~50K headroom in a 200K window for the current prompt + response.

- **`seenMemoryIDs` survives `PrepareNewSession`, not `Reset()`**: When context overflows and a new Claude session starts, already-injected memories are NOT re-injected. This is intentional — Claude already incorporated them in prior turns. Only a full `Reset()` (user-triggered memory wipe) clears this tracking.

- **Subagent tool restriction**: `subagentBaseTools` (`"Read,Write,Edit,Glob,Grep,Bash,mcp__bud2__search_memory"`) explicitly excludes `talk_to_user`, `signal_done`, and most MCP tools. Subagents can read/write files and search memory, but cannot talk to users directly or end executive sessions. Questions route through `AskUserQuestion` interception instead.

- **P1/Background separation**: `ProcessNextP1` and `ProcessNextBackground` run on separate goroutines. A P1 item calls `RequestBackgroundInterrupt()` to signal the background goroutine. The background goroutine uses a `context.CancelFunc` (`backgroundCancel`) that is set before the background session starts. This allows preemption without polling.

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `internal/executive` | `internal/focus` | `PendingItem` for queue, `ContextBundle` for context assembly, `Attention` for salience scoring |
| `internal/executive` | `internal/engram` | Memory retrieval (spread activation), memory eval writes via HTTP client |
| `internal/executive` | `internal/reflex` | Reads `reflex.Log` entries for recent automated handling context |
| `internal/executive` | `internal/budget` | `SessionTracker` for token budget accounting |
| `internal/executive` | Claude SDK | `claudecode.Client` for session management; `WithResume`, `WithMcpServers`, `WithPartialStreaming` options |
| `internal/memory` | `internal/types` | `InboxMessage.ToPercept()` — converts intake messages to the unified `Percept` type |
| `internal/types` | `internal/executive` | `Thread.SessionState` tracks focused/active/frozen; `Percept` is the unit of attention |

## Non-Obvious Behaviors

- **`claudeSessionID` is captured from `StreamEvent`, not `ResultMessage`**: `signal_done` cancels context before `ResultMessage` arrives. If session ID came from `ResultMessage`, every `signal_done` wake would lose the session and start cold next time.

- **`PrepareNewSession()` does NOT clear `seenMemoryIDs`**: The method comment and tests explicitly document this. Only `Reset()` clears them. When a context overflow triggers a new Claude session mid-conversation, the memory deduplication persists — correct behavior, but surprising on first read.

- **`isResuming` skips core identity**: On turns 2+ of the same Claude session, the core identity, full conversation buffer, and wakeup instructions are omitted from the prompt. This is why the prompt is much shorter on resume turns — Claude already has that content in its session history. The `buildPrompt()` code gates these sections on `!isResuming`.

- **Fallback response on missing user reply**: If Claude processes a P1 user message but calls neither `talk_to_user` nor any MCP tool visible to the executive, `processItem` sends a fallback message to the user using `SendMessageFallback`. This is a correctness guard, not an expected path.

- **Subagent ID re-keying**: `SubagentSession.ID` starts as a temporary UUID (generated before spawn). On receiving the first `StreamEvent`, `Spawn()` blocks on `claudeIDReady` (buffered channel, 10s timeout) and re-keys the session in `SubagentManager.sessions` from the temp UUID to the real Claude ID. Any lookup during the brief startup window uses the temp UUID.

- **`active` threads reset to `paused` on load**: `ThreadPool.Load()` detects any threads with `SessionState == active` and resets them to `paused`. This prevents Bud from believing Claude processes from a prior run are still alive after a restart.

## Start Here

- `internal/executive/executive_v2.go` — the main orchestrator: start with `processItem()` to trace the full lifecycle, then `buildContext()` for context assembly. The `ExecutiveV2` struct fields map directly to the subsystems involved.
- `internal/executive/simple_session.go` — session state management: read `PrepareForResume()` vs `PrepareNewSession()` side-by-side with `ShouldReset()` to understand the resume/reset decision tree.
- `internal/executive/simple_session_test.go` — the tests for `ShouldReset` and `HasSeenMemory` document the non-obvious invariants (when `seenMemoryIDs` persists vs clears) more clearly than comments.
- `internal/types/types.go` — all core types: `Thread`, `SessionState`, `Percept`, `InboxMessage`, `Trace`. Reading this file gives you the vocabulary for the entire executive.
- `internal/memory/inbox.go` — `InboxMessage.ToPercept()` — the conversion boundary between raw intake and the attention system; this is where source metadata, intensity, and conversation grouping are assigned.
