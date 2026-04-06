---
topic: Attention & Salience Computation
repo: bud2
generated_at: 2026-04-06T00:00:00Z
commit: 49351784
key_modules: [internal/focus, internal/types]
score: 0.77
---

# Attention & Salience Computation

> Repo: `bud2` | Generated: 2026-04-06 | Commit: 49351784

## Summary

The attention subsystem (`internal/focus`) implements a neurologically-inspired priority and salience model that determines what Bud acts on next. It maintains an ordered set of `PendingItem`s, computes a continuous salience score per item, and selects the next focus target using a combination of hard priority precedence and an arousal-modulated salience threshold — allowing Bud to be more or less responsive depending on recent activity.

## Key Data Structures

### `Priority` (`internal/focus/types.go`)
An `int` type with five levels: `P0Critical=0` (alarms, reminders — preempts everything), `P1UserInput=1` (Discord messages from the owner), `P2DueTask=2` (deadlines, scheduled tasks), `P3ActiveWork=3` (continuation of ongoing work), `P4Exploration=4` (idle ideas). Lower number = higher urgency. Priority is the hard ordering guarantee; salience only matters within the same tier or for the arousal threshold.

### `PendingItem` (`internal/focus/types.go`)
The unit of attention. Fields: `ID`, `Type` (string tag: `user_input`, `due_task`, `reminder`, `idea`), `Priority`, `Salience` (0.0–1.0, computed), `Source` (origin: `discord`, `calendar`, `internal`), `Content`, optional `ChannelID`/`AuthorID`, `Timestamp`, and `Data` (arbitrary payload). Salience is set by `computeSalience` if not pre-populated by the caller.

### `FocusState` (`internal/focus/types.go`)
Snapshot of the current attention state: `CurrentItem` (what's being processed now), `Suspended` (stack of preempted items — first in, last out), `Modes` (list of active `Mode` overrides), and `Arousal` (global activity level, 0.0–1.0, initialized to 0.3).

### `Attention` (`internal/focus/attention.go`)
The core engine. Holds `mu sync.RWMutex`, `state FocusState`, `pending []*PendingItem` (in-memory candidates), and `callback FocusCallback` (called when focus changes). Selection, suspension, resume, and mode management all run through this struct.

### `Queue` (`internal/focus/queue.go`)
A file-persisted priority queue separate from `Attention.pending`. Fields: `items []*PendingItem`, `path string` (disk path), `maxSize int`, `notifyCh chan struct{}` (buffered capacity 1 — signals that a new item is available). Survives daemon restarts. Items are trimmed by age and priority when over capacity.

### `Mode` (`internal/focus/types.go`)
A temporary attention override for a named domain. Fields: `Domain` (e.g., `"gtd"`, `"calendar"`, a specific channel ID), `Action` (e.g., `"bypass_reflex"`, `"debug"`, `"practice"`), `SetBy` (`"executive"` or `"user"`), `ExpiresAt`, optional `Reason`. Used to alter reflex behavior for a bounded time window.

### `ContextBundle` (`internal/focus/types.go`)
The assembled context handed to the executive before a Claude session. Includes `CurrentFocus`, `Suspended`, `BufferContent` (conversation history), `Memories`, `ActiveSchemas`, `ReflexLog`, `CoreIdentity`, and `SubagentQuestions` (pending AskUserQuestion responses from running subagents). Not part of salience computation itself, but is the output of the focus pipeline.

## Lifecycle

1. **Percept or impulse arrives**: External senses (`internal/senses/`) create a `PendingItem` from a Discord message or calendar event. Internal impulses (`Impulse.ToPercept()` in `internal/types/types.go`) convert scheduled tasks, ideas, and autonomous wake triggers into a unified percept form before creating a `PendingItem`.

2. **Queue.Add()**: The item is appended to the persisted `Queue`. If over `maxSize`, `trim()` removes the oldest lowest-priority items. A non-blocking send on `notifyCh` signals that work is waiting — if the channel already has a pending signal, no second signal is sent (capacity-1 buffer prevents stacking).

3. **Attention.AddPending()**: Called (likely from the executive's main loop after reading from Queue) to register the item in-memory. If the item's `Salience` is zero, `computeSalience` is called. `adjustArousal` is then called to update global arousal based on the item's priority.

4. **Salience computation** (`computeSalience`): Starts from a priority-based base (P0 → 1.0, inferred scaling downward). Applies two additive boosts: a source boost for items from `discord` (external human input is weighted up), and a recency boost for items timestamped within the last minute. Result is capped at 1.0.

5. **Arousal adjustment** (`adjustArousal`): High-priority items push arousal up. Arousal decays on each call to `DecayArousal(factor)` (likely called periodically by the executive), with a hard floor of 0.1. Higher arousal lowers the selection threshold, making Bud more responsive.

6. **SelectNext()**: Called when the executive is ready to pick the next focus target. Sorts `pending` by priority ascending then salience descending. Then applies the selection rules in order:
   - **P0 always wins** — selected unconditionally.
   - **P1 user input always wins** (unless P0 present) — bypasses arousal threshold.
   - **System impulses bypass threshold** — autonomous wakes and task impulses already passed the budget gate in `main.go`; re-gating would cause them to silently do nothing (documented in a comment in `attention.go`).
   - **All others**: checked against `getSelectionThreshold()`. Threshold = `0.6 - arousal * 0.3`, ranging from 0.6 (calm) down to 0.3 (fully aroused). Items below threshold are skipped; `nil` is returned if nothing qualifies.

7. **selectItem()**: Removes the chosen item from `pending` by index and returns it.

8. **Focus()**: Sets the selected item as `CurrentItem`. If a current item already exists, it is prepended to the `Suspended` stack (preserving the interrupted task). The `FocusCallback` is fired outside the lock.

9. **Executive runs Claude session**: The focused item drives a Claude Agent SDK session. `ContextBundle` is assembled with focus state, memories, and schemas.

10. **Complete()**: Called when the session ends. `CurrentItem` is cleared. If `Suspended` is non-empty, the top item is automatically popped and re-focused — resuming interrupted work without requiring a new `SelectNext()` cycle.

## Design Decisions

- **System impulses bypass the arousal threshold**: A comment in `attention.go` explains why — autonomous wakes already pass a budget gate in `main.go`. Double-gating would cause autonomous sessions to silently skip when arousal is low, breaking the autonomous cadence.

- **Suspended is a stack, not a single slot**: Multiple items can be preempted in sequence (e.g., P3 task → interrupted by P1 message → interrupted by P0 alarm). Each `Complete()` pops and resumes one level, unwinding the preemption chain in LIFO order.

- **Queue and Attention.pending are separate structures**: `Queue` is the durable, file-backed store that survives restarts. `Attention.pending` is the ephemeral in-memory working set. This separation means the executive controls when items move from persistent queue to active consideration, giving it an opportunity to apply budget or scheduling constraints first.

- **Salience and priority are orthogonal**: Priority is a hard-order guarantee for P0/P1. For P2–P4, salience determines which item among same-priority candidates gets selected, and whether any of them clear the arousal threshold. This means a P3 item from Discord with a recency boost can be selected before an older P3 item from an internal source.

- **Mode expiration is lazy**: `IsAttending()` and `GetActiveMode()` check `IsExpired()` inline for every access. `CleanExpiredModes()` must be called explicitly to remove stale entries from `FocusState.Modes` — they're never auto-pruned during normal selection.

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `internal/senses` | `internal/focus` | Senses create `PendingItem`s and add them to `Queue`; `notifyCh` wakes the executive loop |
| `internal/types` | `internal/focus` | `Impulse.ToPercept()` converts internal impulses into the `Percept` form used to construct `PendingItem`s |
| `internal/focus` | `internal/executive` | `FocusCallback` fires on focus change; `ContextBundle` (assembled from `FocusState`) is passed to `ExecutiveV2` at session start |
| `internal/budget` | `internal/focus` | Budget gate in `main.go` screens autonomous wake impulses before they reach `Attention`; `attention.go` documents that impulses which reach it must not be re-gated |
| `internal/reflex` | `internal/focus` | Reflex evaluation may consume a `PendingItem` before it reaches the executive, preventing it from ever being focused |

## Non-Obvious Behaviors

- **Arousal starts at 0.3, not 0.0**: The system is "moderately alert" at startup. A freshly started daemon will select items with salience ≥ 0.6 − 0.3×0.3 = 0.51, not the theoretical maximum threshold of 0.6. This prevents the daemon from ignoring its first items when arousal hasn't yet built up.

- **User input ignores arousal, system impulses do not (in priority sort)**: Both bypass the *threshold* gate, but the priority-sort still applies. A `P0Critical` alarm preempts a queued `P1UserInput` even if the user typed first.

- **A Discord-source item beats an identical internal-source item**: `computeSalience` adds a source boost for `discord`. Two `P3ActiveWork` items with the same timestamp will have different salience if one came via Discord. This consistently biases selection toward human-visible inputs.

- **notifyCh capacity is 1, not 0**: The buffered channel ensures `Queue.Add()` never blocks the caller, while guaranteeing at least one wake-up signal when items arrive in a burst. Multiple rapid additions do not stack up multiple signals.

- **Suspended items are not re-evaluated for salience on resume**: When `Complete()` pops from `Suspended`, the item's salience value from the time it was originally focused is used. A long-suspended item will not get a recency penalty applied retroactively.

- **`CleanExpiredModes()` is not called automatically**: Modes accumulate in `FocusState.Modes` until explicitly pruned. If the executive doesn't call `CleanExpiredModes()` periodically, `IsAttending()` returns false for expired modes (correct), but the slice keeps growing.

## Start Here

- `internal/focus/types.go` — defines all types: Priority levels, PendingItem, FocusState, Mode, ContextBundle; read this before anything else
- `internal/focus/attention.go` — core engine: `computeSalience`, `adjustArousal`, `SelectNext`, `Focus`, `Complete`; the main logic
- `internal/focus/queue.go` — persisted queue with `notifyCh` signaling; how items enter the system durably
- `internal/types/types.go` — shared types: `Percept`, `Impulse` (with `ToPercept()`), `Thread`, `Arousal`; context for what feeds the attention system
- `cmd/bud/main.go` — where `Attention` and `Queue` are instantiated, wired to senses, and where the budget gate precedes autonomous wake impulses
