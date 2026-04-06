---
topic: Reflex Evaluation Pipeline
repo: bud2
generated_at: 2026-04-06T04:30:00Z
commit: 8e288fb
key_modules: [internal/reflex, internal/senses, internal/types]
score: 0.84
---

# Reflex Evaluation Pipeline

> Repo: `bud2` | Generated: 2026-04-06 | Commit: 8e288fb

## Summary

The reflex engine is bud2's fast-path response layer — a YAML-configurable rule system that intercepts percepts before they reach Claude. When an incoming message matches a reflex's pattern or Ollama classification, it executes a sequential action pipeline (fetch, transform, reply) and returns immediately, avoiding the cost of a full Claude session. If no reflex matches, or if a pipeline explicitly escalates, control passes to the executive.

## Key Data Structures

### `Reflex` (`internal/reflex/types.go`)
The primary rule type. Fields of note:
- `Trigger Trigger` — matching criteria (source, type, regex pattern, or Ollama classifier)
- `Pipeline Pipeline` — ordered slice of `PipelineStep` to execute
- `Level int` — `0`=regex, `1`=heuristic, `2`=ollama, `3`=executive (higher levels are slower)
- `Priority int` — higher fires first when multiple reflexes match
- `Callable bool` — if true, reflex is not auto-matched; only reachable via `invoke_reflex` action
- `FireCount int`, `LastFired time.Time` — persisted stats (stored separately from YAML config)

### `Trigger` (`internal/reflex/types.go`)
- `Pattern string` — regex (compiled lazily to `compiledPattern`)
- `Extract []string` — named capture groups promoted to pipeline variables
- `Source string`, `Type string` — optional filters (e.g., `discord`, `message`)
- `SlashCommand string` — if set, only matches Discord slash commands with this name
- `Classifier string` — `"regex"` (default), `"ollama"`, or `"none"`
- `Intents []string`, `Model string`, `Prompt string` — Ollama classification config

### `PipelineStep` (`internal/reflex/types.go`)
- `Action string` — registered action name (`fetch_url`, `reply`, `gate`, `invoke_reflex`, etc.)
- `Input string` — explicit input variable (e.g., `$url`); if absent, `$_` is used
- `Output string` — variable to store result in; if absent, result goes into `$_`
- `Params map[string]any` — action-specific parameters (inlined in YAML)

### `ReflexResult` (`internal/reflex/types.go`)
Returned by `Execute`. Contains:
- `Success bool`, `Stopped bool` (gate early exit), `Escalate bool`
- `EscalateMessage string`, `EscalateStep int`, `EscalateVars map[string]any` — escalation context snapshot passed upstream to the executive

### `Engine` (`internal/reflex/engine.go`)
The central runtime. Key fields:
- `reflexes map[string]*Reflex` — live registry, guarded by `mu sync.RWMutex`
- `actions *ActionRegistry` — all available action implementations
- `fileModTime map[string]time.Time` — enables hot reload without restart
- `attention AttentionChecker` — when non-nil and active, bypasses reflexes entirely
- `onReply`, `onInteractionReply`, `onReact` — callbacks injected from main.go

### `LogEntry` (`internal/reflex/log.go`)
Per-firing record: `Source`, `Type`, `MatchedBy`, `Actions []string`, `Success`, `Escalated`, `Duration`, `ErrorMessage`. Persisted as JSONL; unsent entries are batched to the executive via `GetUnsent()`.

## Lifecycle

1. **Percept arrives**: `cmd/bud/main.go:processPercept` receives an `*memory.InboxMessage` from a sense callback and calls `engine.Process(ctx, source, typ, content, data)`.

2. **Proactive-mode gate**: If `engine.attention` is set and `IsAttending(source/type/"all")` returns true, `Process` returns `(false, nil)` immediately — the percept bypasses all reflexes and goes directly to the executive. This is how an active Claude session claims a domain.

3. **Slash-command routing**: `Process` inspects `data["slash_command"]`. If present, only reflexes with a matching `SlashCommand` field are candidates. Normal messages match only reflexes without a `SlashCommand` requirement. This prevents cross-contamination.

4. **Candidate selection**: `engine.Match(source, typ, content)` iterates all loaded reflexes, calling `reflex.Match()` on each. `Match()` applies source/type filters first, then checks `Callable` (skips callable reflexes), then runs regex or returns pre-matched for Ollama. Returns a slice of matches sorted by `Priority` descending.

5. **Ollama classification** (if applicable): For each candidate with `Classifier == "ollama"`, `ClassifyWithOllama` sends content to a local Ollama endpoint, gets back an intent string (e.g., `gtd_add`), and stores it in extracted vars as `intent`. The Ollama default model is `qwen2.5:7b`; can be overridden per-reflex.

6. **Pipeline execution** (`Execute`): Variables are initialized from extracted regex groups + `perceptData`. The implicit pipe variable `$_` starts empty. Steps execute sequentially:
   - `reply` and `react` are handled by special engine methods that call `onReply`/`onReact` callbacks
   - All other steps look up the action in `ActionRegistry`, resolve param values via `resolveVar` (substitutes `$varname` references), and call `action.Execute(ctx, params, vars)`
   - Output is stored in `step.Output` if named, otherwise in `$_`
   - If `Execute` returns `ErrStopPipeline`: execution stops cleanly (`Stopped = true`), counts as success
   - If `Execute` returns `ErrEscalate`: execution stops, `Escalate = true` with snapshot of vars at that step

7. **First-match-wins**: `Process` returns `(true, results)` after the first reflex whose pipeline succeeds (or stops via gate). Subsequent lower-priority reflexes are not tried.

8. **Stats persistence**: After each execution, `saveReflexStats` writes `FireCount`/`LastFired` to a separate JSON stats file (`<statePath>/reflex-stats.json`), not back to the YAML config file.

9. **Escalation handoff**: If `Escalate == true` in the result, `processPercept` in main.go routes the percept to the executive along with `EscalateVars` — giving Claude the full pipeline context that triggered escalation.

10. **No match**: If `Process` returns `(false, nil)`, the percept is queued as a focus item and eventually handled by `ExecutiveV2`.

## Design Decisions

- **Stats stored separately from config**: `FireCount`/`LastFired` are written to `reflex-stats.json` rather than back to the YAML files. This avoids overwriting manual edits to reflex config — a real hazard when the engine both reads and writes YAML.

- **Callable reflexes as sub-routines**: `Callable: true` prevents auto-matching. This lets authors define reusable pipeline fragments (e.g., `gtd-add`) that are only invoked by name via `invoke_reflex`, not accidentally fired by incoming messages.

- **Implicit pipe (`$_`)**: Inspired by Unix pipes. Steps that don't name their output write to `$_`; steps that don't name their input read from `$_`. This lets simple linear pipelines avoid boilerplate variable names while still supporting named branches.

- **`invoke_reflex` with template names**: The name param supports Go template syntax — `"gtd-{{.intent}}"` resolves to `"gtd-add"` or `"gtd-list"` at runtime based on Ollama classification. This creates a two-level dispatch: Ollama classifies the intent, then a named sub-reflex implements it.

- **Attention bypass**: When `ExecutiveV2` activates attention on a domain (e.g., `discord`), all reflexes for that domain are skipped. This prevents a reflex from answering a message mid-conversation when Claude is actively engaged — the executive needs to see all incoming messages in context.

- **Hot reload via file mod times**: `CheckForUpdates()` compares file mtime against `fileModTime` cache. Preserved stats from the old reflex (identified by name) are merged into the reloaded version, so firing history survives edits.

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `cmd/bud/main.go` | `internal/reflex` | `engine.Process()` called for every percept; return value determines executive routing |
| `internal/reflex` | `internal/executive` | `ReflexResult.Escalate + EscalateVars` passed upstream when pipeline calls `escalate` action |
| `internal/reflex` | `internal/gtd` | `Engine.gtdStore` interface: task list/add/complete for `gtd_*` actions |
| `internal/reflex` | `internal/integrations/calendar` | `Engine.calendarClient` for `calendar_*` actions (today's events, free/busy) |
| `internal/reflex` | MCP dispatcher | `ToolCaller` interface: `call_tool` action routes arbitrary MCP tool calls through the registered dispatcher |
| `internal/senses/discord.go` | `cmd/bud/main.go` | `onMessage func(*memory.InboxMessage)` callback — senses don't call reflex directly |
| `internal/senses/calendar.go` | `cmd/bud/main.go` | Same callback pattern; daily agenda and meeting reminders become percepts |
| `internal/reflex` | `internal/types` | `Percept`, `Impulse` types used for percept data maps |

## Non-Obvious Behaviors

- **Slash commands and normal messages never share reflexes**: The routing is mutually exclusive at the `Process` level. A reflex without `slash_command` set will never fire for `/foo` invocations, and vice versa. This means reflex authors must create separate rules for slash command variants.

- **`gate` action uses string equality, not boolean**: The condition `"{{.intent}} == not_gtd"` renders via Go template then is evaluated by checking if the left side equals the right side after splitting on ` == `. A rendered result of `"not_gtd == not_gtd"` is truthy; any other output is falsy. There is no numeric comparison or logical operators.

- **Ollama failures fall through**: If `ClassifyWithOllama` returns `not_matched` or an error, the intent is set to `not_matched` and stored in `intent` var. The pipeline still executes — it's up to the reflex author to add a `gate` step that stops on `not_matched`.

- **`invoke_reflex` prevents infinite recursion by depth**: Sub-reflex calls pass a `depth` variable in sub-vars; the action checks for this and returns an error if depth exceeds a threshold. The depth limit is enforced per-call, not globally.

- **`EscalateVars` is a snapshot at the failing step**: When `escalate` fires (or `invoke_reflex` returns `ErrEscalate`), the pipeline variables accumulated up to that step are frozen and attached to the result. The executive receives this context, letting it pick up where the reflex left off rather than starting cold.

- **Log `GetUnsent` marks as sent atomically**: The method advances `lastSent` inside the same lock as reading entries. If the executive crashes before processing, those entries are lost — there is no durable delivery guarantee for reflex log entries.

## Start Here

- `internal/reflex/engine.go` — `Process()` and `Execute()` are the two entry points worth reading front to back; they contain the complete evaluation and execution logic
- `internal/reflex/types.go` — `Reflex`, `Trigger`, `PipelineStep`, `ReflexResult` define the data model; read before engine.go
- `internal/reflex/actions.go` — `ActionRegistry`, built-in action implementations, `ErrStopPipeline`/`ErrEscalate` sentinel errors
- `seed/reflexes/*.yaml` — live reflex definitions; reading a few shows how the YAML maps to the types above
- `internal/reflex/reflex_test.go` — `TestImplicitPiping`, `TestInvokeReflex`, `TestCallableReflexNotAutoMatched` are the best tests for understanding non-obvious behaviors
