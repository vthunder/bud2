---
topic: MCP Tool Dispatch & Registration
repo: bud2
generated_at: 2026-04-06T06:00:00Z
commit: e5fa5f48
key_modules: [internal/mcp, internal/types]
score: 0.99
---

# MCP Tool Dispatch & Registration

> Repo: `bud2` | Generated: 2026-04-06 | Commit: e5fa5f48

## Summary

The MCP layer is bud2's interface to Claude: it exposes ~60+ named tools (state inspection, knowledge graph, communication, subagent management, etc.) as a JSON-RPC server that Claude calls during a session. Tools are registered once at startup by injecting a `Dependencies` struct into category-specific `register*()` functions, and dispatched at runtime through either a stdio transport (for the main executive session) or an HTTP transport (for subagents that need per-session domain routing).

## Key Data Structures

### `Server` (`internal/mcp/server.go`)
The central registry. Holds `handlers map[string]ToolHandler` (function pointers keyed by tool name), `definitions []ToolDef` (schema exposed via `tools/list`), `gkTools map[string]bool` (flags tools that need domain injection), and `sessions map[string]SessionInfo` (token → domain mapping for HTTP mode). Two mutex fields: `sessionsMu sync.RWMutex` for session registry, none for handlers (assumed single-writer at startup).

### `ToolDef` (`internal/mcp/server.go`)
Carries `Name`, `Description`, `Properties map[string]PropDef`, `Required []string`, and a `GKTool bool` flag. The `GKTool` flag is the only per-tool signal that affects runtime behavior (domain injection). All other fields are schema metadata for `tools/list`.

### `Dependencies` (`internal/mcp/tools/deps.go`)
Dependency injection container passed to every `register*()` function. Splits into:
- **Core services** (required): `EngramClient`, `ActivityLog`, `StateInspector`
- **Optional services**: `ReflexEngine`, `GTDStore`, `MemoryJudge`, `CalendarClient`, `GitHubClient`
- **Callback functions** for direct effector access: `SendMessage`, `AddReaction`, `SendFile`, `AddThought`, `SendSignal`, `OnMCPToolCall`
- **GK routing callbacks**: `GKCallTool func(domain, toolName string, args map[string]any)`, `ReadResource func(domain, uri string)`, `RegisterSession func(token, agentID, domain string)`
- **Subagent management callbacks**: `SpawnSubagent`, `ListSubagents`, `AnswerSubagent`, `GetSubagentStatus`, `StopSubagent`, `GetSubagentLog`, `DrainSubagentMemories`

The callback pattern allows the executive to inject live function references without circular imports between `internal/mcp` and `internal/executive`.

### `GKPool` (`internal/mcp/gkpool.go`)
Manages a pool of GK MCP server subprocesses, one per SQLite db file path. `entries map[string]*gkEntry` maps db paths to `ProxyClient` instances. Processes are started on first call and reaped after 5 minutes idle by a background `cleanupLoop()` goroutine.

### `ProxyClient` (`internal/mcp/proxy.go`)
Wraps a stdio MCP subprocess (`exec.Cmd` + `stdin`/`stdout` pipes). `mu sync.Mutex` serializes all send/receive operations — the JSON-RPC protocol is strictly request/response over a single stream, so only one in-flight call is safe at a time. `nextID int64` is the atomic request counter.

### `SessionInfo` (`internal/mcp/server.go`)
`{ AgentID string, DefaultDomain string }` — stored in `Server.sessions` keyed by a 16-byte hex token. The token is embedded in the subagent's MCP URL (`/mcp/{token}`) and auto-injected into every GK tool call that omits the `domain` argument.

## Lifecycle

### Registration (startup, once)

1. **Bootstrap `Server`**: `mcp.NewServer()` initializes empty `handlers`, `definitions`, `gkTools`, and `sessions` maps.

2. **Build `Dependencies`**: The executive assembles a `tools.Dependencies` struct, injecting live service references and callback functions. This happens after all services (Engram, ActivityLog, reflex engine, etc.) are initialized.

3. **`RegisterAll(server, deps)`** (`internal/mcp/tools/register.go`): The single entry point that calls every category-specific registration function:
   - `registerCommunicationTools` — `talk_to_user`, `discord_react`, `send_image`, `signal_done`, `save_thought`
   - `registerMemoryTools` — `list_traces`, `search_memory`, `get_schema`, `query_episode`
   - `registerActivityTools` — `journal_*`, `activity_*`
   - `registerStateTools` — `state_summary`, `state_health`, `state_traces`, `state_sessions`, `state_percepts`, `state_threads`, `state_logs`, `state_queues`, `memory_flush`, `memory_reset`, `trigger_bud_redeploy`
   - `registerGTDTools` — `gtd_add`, `gtd_list`, `gtd_complete`, `gtd_update`, `gtd_areas`, `gtd_projects`
   - `registerReflexTools` — `create_reflex`, `list_reflexes`, `delete_reflex`
   - `registerCalendarTools` — `calendar_today`, `calendar_upcoming`, `calendar_list_events`, `calendar_free_busy`, `calendar_get_event`, `calendar_create_event`
   - `registerGitHubTools` — `github_list_projects`, `github_get_project`, `github_project_items`
   - `registerSubagentTools` — `Agent_spawn_async`, `list_subagents`, `list_jobs`, `get_subagent_status`, `get_subagent_log`, `stop_subagent`, `answer_subagent`, `approve_subagent_memories`
   - `registerEvalTools` — `memory_judge_sample`
   - `registerProjectTools` — `list_projects`, `create_project`
   - `registerImageGenTools` — `generate_image`
   - `registerVMBrowserTools` — `vm_start`, `vm_stop`, `vm_screenshot`, `vm_click`, `vm_type`, `vm_key`, `vm_actions`, `browser_*`, `vm_*` (20+ VM/browser tools)

4. **`RegisterGKTools(server, deps)`** (`internal/mcp/tools/gk.go`): Registers 27 GK tools (4 tiers: build, search, navigate, maintenance). Each tool is registered with `GKTool: true` in its `ToolDef`, which tags it in `server.gkTools`.

5. **`RegisterResourceTools(server, deps)`** (`internal/mcp/tools/resource.go`): Registers `read_resource` and `list_resources` tools that route to the `deps.ReadResource` callback.

6. **External proxy registration** (optional): `StartProxiesFromConfig(mcpConfigPath, server)` reads `.mcp.json`, starts a `ProxyClient` for each stdio server entry, calls `client.DiscoverTools()`, and registers forwarding handlers for each discovered tool. HTTP-type servers in `.mcp.json` are skipped (Claude connects to them directly).

### Dispatch (stdio mode, each tool call)

7. **`Run()`** reads stdin line-by-line. Each line is a JSON-RPC request (`jsonRPCRequest{JSONRPC, ID, Method, Params}`).

8. **`handleRequest(req)`** routes by `Method`:
   - `initialize` / `notifications/initialized` → capability negotiation
   - `tools/list` → `handleToolsList()` — converts `definitions` to MCP `toolDefinition` format
   - `tools/call` → `handleToolsCall()`
   - `resources/list`, `resources/read` → resource handlers (domain always "/" in stdio mode)

9. **`handleToolsCall(req)`** unmarshals params to `toolsCallParams{Name, Arguments}`, looks up `handlers[name]`, invokes the handler with `(ctx, args)`, wraps the string result in `toolsCallResult{Content: [{Type:"text", Text:result}], IsError: false}`.

10. **Handler executes**: The tool handler closure captures `deps` at registration time. It reads from deps services, may call effector callbacks (e.g. `deps.SendMessage()`), and returns a string result.

### Dispatch (HTTP mode, with session domain routing)

11. **`RunHTTP(addr)`** starts `net/http` listening at `addr`. All requests to `/mcp` and `/mcp/{token}` are handled by `handleHTTP()`.

12. **`handleHTTP(w, r)`** extracts the session token from the path, looks up `SessionInfo` via `DomainForToken(token)` (defaults to "/" if token unknown). Parses the JSON-RPC body.

13. **GK domain injection**: If `Method == "tools/call"` and the named tool is in `server.gkTools`: if "domain" is absent from the arguments, it is injected from the session's `DefaultDomain` before dispatching. The params are re-marshaled with the injected domain.

14. **Resource routing**: For `resources/list` and `resources/read`, the session domain (from token) is passed directly to the registered resource provider callbacks.

### Session token lifecycle (for subagents)

15. **`Agent_spawn_async` tool** (`register.go:registerSubagentTools`): When invoked, it calls `generateToken()` (16-byte hex), builds a tokenized URL `{MCPBaseURL}/mcp/{token}`, calls `deps.RegisterSession(token, "", domain)` to pre-register the domain, then calls `deps.SpawnSubagent(..., mcpURL)`. After the subagent ID is returned, it calls `deps.RegisterSession(token, agentID, domain)` again to backfill the agent ID.

16. The subagent receives the tokenized URL as its MCP endpoint. Every `gk_*` call it makes arrives at `/mcp/{token}`, triggering automatic domain injection (step 13). The subagent never needs to specify a domain explicitly.

## Design Decisions

- **Callback injection over direct imports**: `Dependencies` uses function-valued fields (e.g. `SendMessage func(...)`) rather than interface types or direct imports of `internal/executive`. This prevents import cycles — `internal/mcp/tools` imports `internal/executive` for type references, but the executive's live methods are injected as closures rather than via an interface.

- **Single-mutex stdio serialization**: `ProxyClient.mu` serializes all JSON-RPC send/receive operations on a single mutex. MCP stdio is inherently serial (request → response), so parallel calls to the same proxy client would corrupt the stream. Callers that need concurrent access must use the `GKPool`, which maintains one `ProxyClient` per db path.

- **GK domain flag over per-call routing**: Rather than routing by URL path or argument inspection at every call, the `GKTool bool` flag is set once at registration and stored in `server.gkTools`. The HTTP handler has an O(1) lookup. This keeps the hot path simple at the cost of a compile-time classification decision.

- **5-minute GK process idle reaping**: `GKPool.cleanupLoop()` runs a background goroutine. Processes are kept alive for 5 minutes after last use to amortize subprocess startup cost (bun + SQLite open). This is a tunable but hardcoded value — not configurable via `Dependencies`.

- **`signal_done` bypass queueing**: When `deps.SendSignal` callback is set (direct delivery), `signal_done` does not write to a file queue. The callback is expected to deliver synchronously. This avoids queueing latency at session boundaries where `signal_done` must fire before the process terminates.

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `internal/mcp/tools` | `internal/executive` | Subagent callbacks (`SpawnSubagent`, `ListSubagents`, etc.) injected as closures into `Dependencies` at startup |
| `internal/mcp/tools` | `internal/engram` | `EngramClient *engram.Client` in `Dependencies`; used by memory tools (`search_memory`, `query_episode`) |
| `internal/mcp/tools` | `internal/reflex` | `ReflexEngine *reflex.Engine` in `Dependencies`; used by `create_reflex`, `list_reflexes`, `delete_reflex` |
| `internal/mcp/tools` | `internal/activity` | `ActivityLog *activity.Log` in `Dependencies`; used by all `journal_*` and `activity_*` tools |
| `internal/mcp/tools` | `internal/state` | `StateInspector *state.Inspector`; used by `state_summary`, `state_health`, `state_percepts`, `state_threads` |
| `internal/mcp/tools` | `internal/gtd` | `GTDStore gtd.Store`; used by all `gtd_*` tools |
| `internal/mcp` | `internal/mcp/tools` | `Server` passed into `RegisterAll()`, `RegisterGKTools()`, `RegisterResourceTools()` |
| `internal/mcp` (GKPool) | GK subprocess | `ProxyClient` over stdin/stdout; JSON-RPC `tools/call` and `resources/read` forwarded verbatim |
| `internal/mcp` (Server) | Claude (HTTP client) | HTTP POST to `/mcp/{token}`; standard MCP JSON-RPC protocol |

## Non-Obvious Behaviors

- **`GKTool` flag only affects HTTP mode**: In stdio mode, GK tools dispatch exactly like any other tool. The domain injection happens exclusively in `handleHTTP()`. In stdio mode, the "domain" argument must be provided explicitly by the caller (or omitted to use the GK default, which GK itself handles).

- **`deps.OnMCPToolCall` hook for user response detection**: After `talk_to_user` and `discord_react` send their content, they call `deps.OnMCPToolCall(toolName)`. This is used by the executive to detect whether Claude actually produced a user-visible response, enabling validation logic that checks "did Claude speak?" independently of Claude's text output.

- **Proxy tools from `.mcp.json` are indistinguishable to Claude**: `StartProxiesFromConfig()` registers forwarding handlers under the same `server.RegisterTool()` API. Claude sees them as first-class tools in `tools/list`. There is no namespace separation — if an external server registers a tool named `foo` and a built-in tool is also named `foo`, the last registration wins (no error).

- **Session token pre-registration before spawn**: `RegisterSession(token, "", domain)` is called before `SpawnSubagent()` returns, with an empty `agentID`. If the subagent immediately makes a GK call before the post-spawn `RegisterSession` update, the domain is still correct (the domain is set), but `AgentID` would be empty in the session registry. This is a window condition.

- **`server.Call()` bypasses JSON-RPC entirely**: The reflex engine can invoke tools synchronously via `server.Call(toolName, args)` without going through stdio or HTTP. This shares the same handler map but skips all protocol framing. Errors from the handler propagate as Go errors rather than JSON-RPC error responses.

- **`memory_reset` coordinates across three layers**: The tool writes a "reset pending" flag file, signals the main process to clear the in-memory conversation buffer, sleeps briefly, then clears the on-disk buffer, then fires the `SendSignal` callback. This sequencing exists to prevent a race where the buffer clear happens before the signal is processed, or the conversation resumes before the reset completes.

## Start Here

- `internal/mcp/server.go` — the `Server` type and `RegisterTool()` method; understand this before reading any tool registration code
- `internal/mcp/tools/deps.go` — the `Dependencies` struct; every tool handler captures from this at registration time
- `internal/mcp/tools/register.go` — `RegisterAll()` and the category-level `register*()` functions; the map from tool name to handler
- `internal/mcp/tools/gk.go` — `RegisterGKTools()` and the `gkForward()` function; how GK multi-domain routing works at the tool level
- `internal/mcp/server.go:handleHTTP()` — the HTTP dispatch path including domain injection; the critical difference from stdio dispatch
