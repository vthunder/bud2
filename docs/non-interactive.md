Migration: Interactive tmux → Non-interactive -p mode
What changes

1. Remove tmux layer entirely
 (HIGH impact)
- internal/executive/tmux.go (192 lines) — delete entirely
- simple_session.go — remove SendPrompt() (interactive), EnsureRunning() tmux startup, PID file polling, pane capture, keystroke sending
- No more tmux send-keys for prompt delivery
- No more tmux capture-pane for output checking

2. Replace with subprocess execution
 (HIGH impact)
- Keep/enhance SendPromptPrint() which already exists and works
- Each prompt: spawn claude -p <prompt> --session-id <id> --output-format stream-json --dangerously-skip-permissions
- Parse stdout stream-json line by line (already implemented)
- Process exits when done — no persistent background process

3. CPU idle detection → no longer needed
 (MEDIUM)
- internal/budget/cpuwatcher.go — can remove
- Currently monitors Claude process CPU to detect when it's "done thinking"
- With -p mode, process exit = done. Clean signal.

4. Session state tracking — needs adjustment
 (MEDIUM)
- seenMemoryIDs, memoryIDMap, lastBufferSync currently live in memory
- These survive fine because SimpleSession object persists across -p invocations
- Session content size check (ShouldReset()) still works — reads the session JSONL file
- --session-id flag preserves Claude's conversation history across invocations

5. Environment variable passing — simplify
 (LOW)
- Currently: exports env vars into tmux shell before running claude
- New: just set env vars on the exec.Command directly (cleaner)

What stays the same

- Prompt building — buildContext() / buildPrompt() unchanged
- Memory tracking — seenMemoryIDs, memoryIDMap unchanged
- MCP tools — Claude CLI reads .mcp.json in both modes, tools work the same
- Executive loop — processItem() flow identical, just calls different send method
- Callbacks — OnOutput, OnToolCall already work with stream-json parsing
- Session reset logic — content size threshold, fresh session ID generation

What we gain
- No tmux dependency (simpler infra)
- Clean process lifecycle (spawn → stream → exit)
- Reliable output capture (stdout vs tmux pane scraping)
- No PID tracking or process state machine
- No startup polling ("is Claude ready yet?")
- Easier testing (mock subprocess vs mock tmux)

What we lose (and mitigations)
| Lost | Mitigation |
|------|------------|
| Persistent process between prompts | --session-id preserves context in session file |
| Real-time status checking | Process exit = done, no need to poll |
| Interactive debugging (attach to tmux) | Can still run claude interactively for debugging |
| Startup cost amortization | Each -p call has ~2-3s startup, but avoids session staleness issues |

Files affected
| File | Action | Size |
|------|--------|------|
| internal/executive/tmux.go | Delete | 192 lines |
| internal/executive/simple_session.go | Major rewrite — remove interactive path, simplify to -p only | 827 lines → ~400 |
| internal/executive/executive_v2.go | Minor — remove UseInteractive config, always use -p | ~5 lines |
| internal/budget/cpuwatcher.go | Delete | ~150 lines |
| internal/executive/claude.go | Delete or archive — old v1 session, no longer needed | 820 lines |
| cmd/bud/main.go | Minor — remove cpuwatcher setup, simplify session init | ~20 lines |
| .mcp.json generation | No change — works in both modes |

Estimated scope
~1200 lines deleted (tmux.go, cpuwatcher.go, claude.go)
~400 lines simplified in simple_session.go
~25 lines changed in executive_v2.go + main.go
Net reduction: probably 800-1000 lines of code

Key question before proceeding
The -p mode spawns a new process per prompt. With --session-id, Claude reloads the session file each time (~2-3s startup). Is that acceptable latency, or do we need to optimize? The current tmux approach has near-zero per-prompt overhead since the process is already running.

---

## Session Metrics (stream-json output)

The Claude CLI provides detailed usage data in stream-json mode that we should capture.

### Final `result` event fields

| Field | Type | Description |
|---|---|---|
| `total_cost_usd` | float | Actual dollar cost for entire session |
| `duration_ms` | int | Wall-clock duration |
| `duration_api_ms` | int | API-only duration (excludes tool execution) |
| `num_turns` | int | Number of agentic turns |
| `usage.input_tokens` | int | Non-cached input tokens |
| `usage.output_tokens` | int | Output tokens generated |
| `usage.cache_creation_input_tokens` | int | Tokens written to cache |
| `usage.cache_read_input_tokens` | int | Tokens read from cache |
| `usage.service_tier` | string | "standard" or other tier |
| `modelUsage.<model>.contextWindow` | int | Max context window |
| `modelUsage.<model>.maxOutputTokens` | int | Max output tokens |

### Per-turn `assistant` events

Each `assistant` event includes a `message.usage` block with per-turn token counts:
```json
{"type":"assistant","message":{"usage":{"input_tokens":2,"cache_creation_input_tokens":2817,"cache_read_input_tokens":7175,"output_tokens":1}}}
```

### Useful CLI flags

- `--max-budget-usd <amount>` — hard cost cap per invocation (only in `--print` mode)
- `--verbose` — required for stream-json to include per-turn usage
- `--debug-file <path>` — raw API request/response logging

### Local stats file

`~/.claude/stats-cache.json` contains cumulative stats:
- `dailyActivity[]` — per-day message/session/tool counts
- `dailyModelTokens[]` — per-day output tokens by model
- `modelUsage` — lifetime totals by model
- `totalSessions`, `totalMessages`

### Current gap

The `result` event handler in `simple_session.go:325` only extracts the text result
string. The `StreamEvent` struct has `CostUSD`/`TotalCost` fields but they use wrong
JSON keys (camelCase vs CLI's snake_case) and are never read.

### Implementation plan

1. Add `SessionUsage` struct with all metric fields
2. Parse full `result` event in `handleStreamEvent`
3. Expose usage via callback or return value from `Run()`
4. Use actual cost for budget decisions (replace time-based proxies)
5. Optionally track per-turn token counts from `assistant` events
6. Consider reading `stats-cache.json` for cumulative reporting
