---
topic: Subagent Orchestration
repo: bud2
generated_at: 2026-04-06T00:00:00Z
commit: b18dfd36
key_modules: [internal/executive, internal/types, internal/effectors]
score: 0.98
---

# Subagent Orchestration

> Repo: `bud2` | Generated: 2026-04-06 | Commit: b18dfd36

## Summary

Subagent orchestration lets the executive spawn long-lived Claude subprocess sessions that work autonomously on delegated tasks while the main executive stays responsive to user input. Questions from subagents are routed to the user through the executive without restarting the subagent session, and structured output from completed subagents is parsed and auto-ingested into Engram before the executive reviews the result.

## Key Data Structures

### `SubagentSession` (`internal/executive/subagent_session.go`)

The live state for a single subagent run. Holds lifecycle status, staged memories, an event log, and the channels used for question routing.

Key fields:
- `ID string` — starts as a temp UUID, is re-keyed to the Claude-assigned session ID once the first `StreamEvent` arrives (typically within milliseconds).
- `status SubagentStatus` — one of `Running`, `WaitingForInput`, `Completed`, `Failed`, `Stopped`.
- `answerReady chan string` — buffered(1); receives answers from the executive so `Answer()` never blocks.
- `claudeIDReady chan string` — buffered(1); `Spawn()` blocks on this (up to 10s) to receive the real Claude session ID before returning.
- `stagedMemories []StagedMemory` — `save_thought` calls from the subagent are held here pending executive approval before being written to Engram.
- `events []SubagentEvent` — ring buffer capped at `maxSubagentEvents` (50); captures tool calls, text output, errors.
- `cancel context.CancelFunc` — cancels the subagent's task context to terminate the Claude subprocess.
- `stopped bool` — set by `Stop()` before cancelling so `runSession` can distinguish explicit stop from unexpected cancellation.

### `SubagentManager` (`internal/executive/subagent_session.go`)

Registry and lifecycle manager for all active subagent sessions.

Key fields:
- `sessions map[string]*SubagentSession` — keyed by session ID (temp UUID at spawn, then Claude session ID once known).
- `QuestionNotify chan *SubagentSession` — sends to the executive when a subagent calls `AskUserQuestion`.
- `DoneNotify chan *SubagentSession` — sends to the executive when a subagent completes, fails, or is stopped.
- A background `cleanupLoop` goroutine removes finished sessions older than 1 hour every 10 minutes.

### `SubagentConfig` (`internal/executive/subagent_session.go`)

Parameters passed to `SubagentManager.Spawn()`.

Key fields:
- `AllowedTools string` — restricts built-in tools; the base default is `subagentBaseTools = "Read,Write,Edit,Glob,Grep,Bash,mcp__bud2__search_memory"`.
- `AgentDefs map[string]claudecode.AgentDefinition` — registered agent definitions so the SDK's built-in `Agent` tool can resolve `"namespace:name"` references without file management.
- `MCPServerURL string` — required for the subagent to call `signal_done` and other bud2 MCP tools.

### `AgentOutput` (`internal/executive/executive_v2.go`)

Structured JSON schema that subagents may emit at the end of their response.

```go
type AgentOutput struct {
    AgentID      string
    TaskRef      string
    Level        string
    Observations []AgentObservation
    Next         *AgentNext
    Principles   []PrincipleEntry
}
```

Bud auto-posts `Observations` and `Principles` to Engram before forwarding the full schema to the executive for review.

### `Agent` (`internal/executive/profiles.go`)

Agent definition loaded from plugin directories (`state/system/plugins/<namespace>/agents/<name>.yaml`).

Key fields:
- `Skills []string` — skill names resolved from `state/system/plugins/<ns>/skills/` or `state/system/skills/`.
- `Tools []string` — additional tools beyond the base set.
- `Body string` — parsed from markdown body after YAML frontmatter; prepended to skill content in the system prompt.

### `WorkflowInstance` (`internal/executive/workflow.go`)

Tracks live state of a multi-step workflow. When a subagent is part of a workflow, `SubagentSession.WorkflowInstanceID` and `WorkflowStep` are set, and results are stored in `WorkflowInstance.Outputs` keyed by step ID for use in subsequent step context templates.

## Lifecycle

1. **Spawn request**: An MCP tool call (typically `Agent_spawn_async`) invokes the `spawnFn` callback returned by `ExecutiveV2.SubagentCallbacks()`. The callback calls `ResolveSubagentConfig()` to merge agent tools with the base tool set and concatenate skill content into a system prompt append.

2. **Session creation**: `SubagentManager.Spawn()` creates a `SubagentSession` with a temp UUID, starts `runSession()` in a goroutine, then blocks up to 10 seconds on `claudeIDReady` waiting for the real Claude session ID from the first `StreamEvent`.

3. **Session ID re-keying**: When `runSession` receives the first `StreamEvent`, it sends the Claude-assigned session ID on `claudeIDReady`. `Spawn()` unblocks, removes the temp UUID from `sessions`, and re-inserts under the real ID. The session is now addressable by Claude session ID for `answer`, `status`, `stop`, and `get_log` operations.

4. **Subagent execution**: `runSession()` calls the Claude Agent SDK with `CanUseTool` set to intercept `AskUserQuestion`. The subagent runs autonomously, calling MCP tools and writing output. The `receiveLoop()` shared in `session_core.go` streams events and fires callbacks for text, thinking, tool calls, and the final result.

5. **Question routing — blocking on the subagent side**: When the subagent calls `AskUserQuestion`, the `CanUseTool` hook fires synchronously inside the SDK's tool-dispatch loop. The hook extracts the question text via `extractAskUserQuestionText()`, sets `session.pendingQuestion`, transitions status to `WaitingForInput`, and sends the session on `QuestionNotify`. It then blocks on `answerReady`.

6. **Question routing — executive side**: `watchSubagentQuestions()` listens on `QuestionNotify` and adds a P2 focus item to the queue. On the next executive wake, `buildContext()` populates `SubagentQuestions` in the context bundle. `buildPrompt()` renders these so Claude sees which subagent is waiting and what it asked. Claude relays the question to the user via `talk_to_user`.

7. **Answer delivery**: When the user replies, the executive calls `SubagentManager.Answer()`, which looks up the session and sends the answer on `answerReady` (buffered(1) — never blocks). The `CanUseTool` hook unblocks and returns a `Deny` response whose message body contains the answer text. The SDK delivers this as the tool's result to the subagent — no subprocess restart.

8. **Completion**: When `runSession` finishes (result or error), it sets `session.status` accordingly and sends the session on `DoneNotify`. `watchSubagentDone()` creates a P3 focus item so the executive is woken to review the result.

9. **Structured output processing**: On the next executive wake, `parseAgentOutput()` scans the result string for the last `\`\`\`json` fence or bare `{...}` block matching the `AgentOutput` schema. If found, observations are posted to Engram and principles are stored with the `"principle"` tag before the full schema is forwarded to the executive for `next.action` evaluation.

10. **Memory approval**: Calls to `save_thought` during the subagent session are intercepted and stored in `session.stagedMemories`. The executive drains these via `DrainStagedMemories()` and writes approved thoughts to Engram. Unapproved thoughts are discarded.

## Design Decisions

- **CanUseTool intercept instead of subprocess restart**: Question routing uses the SDK's `CanUseTool` hook rather than terminating and restarting the subagent. This preserves full session state and token context across the pause/resume, avoiding the cost and fragility of re-injection.

- **Buffered(1) answerReady**: `answerReady` is buffered so `Answer()` returns immediately even if the subagent's goroutine hasn't unblocked yet. This means `Answer()` can be called safely from any goroutine without deadlock risk.

- **Temp UUID → Claude session ID re-keying**: Subagents are externally addressable by their Claude session ID so `get_subagent_log` and `answer_subagent` work without a separate Bud-assigned ID. The 10-second wait in `Spawn()` makes the real ID available to the caller before returning.

- **Staged memory approval**: `save_thought` calls are never written to Engram directly during a subagent session. This prevents low-quality or hallucinated observations from entering the memory store without human-in-the-loop review. The executive (and ultimately the owner) decides what gets promoted.

- **Agent defs loaded fresh per spawn**: `loadAgentDefs()` scans `state/system/plugins/` on every `Spawn()` call. This means changes to agent YAML files take effect immediately without restarting Bud.

- **subagentBaseTools constant**: The default restricted tool set is declared as a constant in `executive_v2.go` and passed through `SubagentCallbacks()`. Agent profiles extend this set; they cannot subtract from it. The base set intentionally excludes `talk_to_user`, `signal_done`, and Discord tools — subagents communicate through structured output, not direct Discord access.

- **P1/background concurrency**: The executive runs P1 (user input) and background (P2+ autonomous wakes) sessions on separate goroutines via `ProcessNextP1` and `ProcessNextBackground`. When a P1 item arrives, `RequestBackgroundInterrupt()` cancels any running background session. Subagents are not affected — they run independently of the executive session loop.

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `internal/executive` | `internal/mcp/tools` | `SubagentCallbacks()` returns closures injected into the MCP tools dependency struct — spawn, answer, status, stop, getLog, drainMemories |
| `internal/mcp/tools` | `internal/executive` | `Agent_spawn_async` and related tools invoke these callbacks to create and manage subagent sessions |
| `internal/executive` | Claude Agent SDK (`severity1/claude-agent-sdk-go`) | `SubagentManager.runSession()` calls the SDK with `CanUseTool`, `WithMcpServers`, `WithAllowedTools`, and `WithAgents` options |
| `internal/executive` | `internal/engram` | Structured observations and principles from `AgentOutput` are posted to Engram via `engram.Client` before executive review |
| `internal/executive` | `internal/focus` | `watchSubagentQuestions()` and `watchSubagentDone()` inject `PendingItem`s into the focus queue to wake the executive |
| `internal/executive` | `internal/effectors` | Effectors are not called by subagents directly; subagent output reaches Discord only after the executive processes the P3 focus item |

## Non-Obvious Behaviors

- **Deny = answer**: The `CanUseTool` hook returns a `Deny` verdict with the answer embedded in the message. From Claude's perspective it looks like a permission denial — but the denial message body is the user's answer. Claude is trained to treat this as the tool result and continue. There is no "allow with modified input" flow used here.

- **Memory retrieval skipped for autonomous wakes**: `buildContext()` skips Engram memory retrieval entirely for autonomous wake focus items. A data analysis found 48% of retrieved memories rated 1/5 in wake contexts, dragging precision to ~30%. Subagent-done P3 items are processed as autonomous wakes, so the executive reviewing subagent output does so without injected memories.

- **Session cleanup is passive**: Finished sessions are not removed immediately on completion. The `cleanupLoop` goroutine runs every 10 minutes and removes sessions older than 1 hour. During that window, `get_subagent_log` and `status` queries still work on completed sessions.

- **Workflow outputs are raw JSON**: `WorkflowInstance.Outputs` stores `json.RawMessage` per step. `RenderContextTemplate()` parses these into `map[string]any` for Go template rendering — meaning downstream steps access prior step output via dot-path notation (`{{jsonPath "direction.title" .outputs.strategy}}`), not typed structs.

- **CanUseTool blocks the SDK dispatch thread**: The question hook blocks the goroutine running the SDK's internal tool dispatch. This means the subagent Claude subprocess is truly paused — no partial tool results or racing callbacks — until `answerReady` is signalled.

- **AgentOutput parsed with fallback**: `parseAgentOutput()` first looks for the last ` ```json ` fence in the result. If none, it progressively tries smaller substrings starting from the last `{` character. This tolerates agents that embed the schema without a code fence, but means trailing non-JSON `{` characters in the output can cause spurious parse attempts.

## Start Here

- `internal/executive/subagent_session.go` — core of spawn, question routing, and lifecycle; read top-down for the full flow including `CanUseTool` hook logic
- `internal/executive/executive_v2.go` — `SubagentCallbacks()` to see how MCP tools connect; `watchSubagentQuestions()` and `watchSubagentDone()` to see how completion wakes the executive
- `internal/executive/profiles.go` — `ResolveSubagentConfig()` and `LoadAgent()` to understand how agent definitions and skills compose into a system prompt
- `internal/executive/agent_defs.go` — `LoadAllAgents()` to see how plugin-based agent definitions are registered with the SDK
- `internal/executive/workflow.go` — `WorkflowInstance`, `NextStep()`, and `RenderContextTemplate()` for multi-step workflow sequencing
