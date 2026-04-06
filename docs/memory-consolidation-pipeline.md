---
topic: Memory Consolidation Pipeline
repo: bud2
generated_at: 2026-04-06T10:00:00Z
commit: b4d9970
key_modules: [internal/engram, internal/memory, internal/embedding, internal/eval]
score: 0.33
---

# Memory Consolidation Pipeline

> Repo: `bud2` | Generated: 2026-04-06 | Commit: b4d9970

## Summary

The memory consolidation pipeline is the system by which raw conversational episodes are ingested into Engram (the external memory server), linked into a graph structure, and eventually compressed into consolidated traces. Bud2's role is to ingest episodes and retrieve traces; consolidation itself is fully delegated to the Engram service. A local `TracePool` in `internal/memory/` provides an in-process activation cache distinct from Engram's persistent store.

## Key Data Structures

### `Episode` (`internal/engram/client.go`)
Tier-1 raw memory: an individual message, response, or thought as stored in Engram. Fields include `ID`, `Content`, `TokenCount`, `Source` (e.g. `"discord"`, `"bud"`, `"agent:coder"`), `Author`, `Channel`, `TimestampEvent`, `Embedding` (optional, pre-computed by caller), `ReplyTo`, and `Attachments`. `DialogueAct` and `EntropyScore` are populated by Engram's NER pipeline.

### `Trace` (`internal/engram/client.go`)
Tier-3 consolidated memory, called "engram" in server responses. Fields: `ID`, `Summary` (human-readable compressed text), `Level` (compression depth: 0=raw summary, 4/8/16/32=word-count targets for pyramid summaries), `EventTime`, `SchemaIDs` (recurring-pattern annotations). Retrieved via semantic search or direct lookup.

### `TracePool` (`internal/memory/traces.go`)
In-process short-term trace cache. Holds `types.Trace` structs in a `sync.RWMutex`-protected map and persists them to a JSON file. Implements local spreading activation (`SpreadActivation`, `DecayActivation`, `PruneWeak`), cosine-similarity search (`FindSimilar`), and a "core" concept (identity traces that survive `ClearNonCore`). This is entirely separate from Engram's persistent store.

### `IngestEpisodeRequest` (`internal/engram/client.go`)
The payload sent to `POST /v1/episodes`. The caller optionally pre-computes and includes the `Embedding` field — if absent, Engram embeds the content itself. `ReplyTo` carries the ID of the previous episode to enable the conversation graph.

### `TraceContext` (`internal/engram/client.go`)
Enriched trace fetched via `GET /v1/engram/{id}/context`. Contains the `Trace`, its `Sources` (original episodes), and `Entities` (NER-extracted named entities linked to it). Used to build deep retrieval context in sessions.

### `Judge` (`internal/eval/judge.go`)
Independent memory quality evaluator. Reads `activity.jsonl` for `memory_eval` entries (session self-ratings) and `executive_wake` entries (query context), then re-rates each trace using an LLM prompt (`judgePrompt`) and computes bias, Pearson correlation, and agreement against the session's own ratings.

### `SampleReport` (`internal/eval/judge.go`)
Output of a batch `Judge.EvaluateSample` run. Fields: `SelfAvg`, `JudgeAvg`, `Bias` (judge − self), `Correlation`, `Agreement` (% within 1 point), `Results`, and `Outliers` (|difference| ≥ 2).

## Lifecycle

1. **Episode ingestion — Discord messages**: `cmd/bud/main.go:storeEpisode` fires on each incoming `InboxMessage` of type `"message"`. It builds an `IngestEpisodeRequest` from the message fields and calls `engramClient.IngestEpisode(POST /v1/episodes)`. On success, it calls `engramClient.AddEpisodeEdge(prevID, result.ID, "follows", 1.0)` to link the new episode to the previous one in the same channel, building a directed conversation graph.

2. **Episode ingestion — Bud responses**: `captureResponse` in `cmd/bud/main.go` calls `IngestEpisode` with `Source: "bud"` whenever Bud sends a message. This keeps Bud's own outputs in the episode store for future retrieval.

3. **Episode ingestion — thoughts**: The `save_thought` MCP tool calls `deps.AddThought(content)`, which routes through `processInboxMessage` with `Subtype: "thought"`. The same `storeEpisode` path fires, tagging the episode `Source: "discord"` with the thought as content. Subagent observations are ingested directly in `executive_v2.go:~line 499` via `IngestEpisode` with `Source: "agent:<agentID>"` when a subagent completes.

4. **Consolidation (Engram-side)**: Bud2 does **not** trigger consolidation. The `engram.Client.Consolidate()` method exists but is never called from the main binary or executive. Engram consolidates episodes into traces automatically on its own schedule. The `memory_flush` MCP tool logs the message "Engram handles consolidation automatically" and returns immediately — it is effectively a no-op.

5. **Retrieval during context assembly**: `ExecutiveV2.buildConversationBuffer` (executive_v2.go ~line 1207) calls `e.memory.Search(query, limit, level)` to fetch semantically relevant traces. It also calls `GetUnconsolidatedEpisodeIDs(channelID)` to obtain the set of episodes not yet linked to any trace. Episodes 31–100 in the conversation buffer are filtered to unconsolidated-only (at compression level C8), ensuring no content falls through the gap between consolidation cycles.

6. **Pyramid summary retrieval**: `GetEpisodeSummariesBatch(episodeIDs, level)` fetches compressed summaries at a specific word-count target (4/8/16/32). `GetTrace(id, level)` can request a trace at a specific compression depth. These pyramid levels are computed and cached by Engram; bud2 just selects the desired level at read time.

7. **Activation spreading (local)**: `TracePool.SpreadActivation(emb, boost, threshold)` computes cosine similarity between the given embedding and all local traces, then boosts activation proportionally. `DecayActivation(rate)` applies exponential decay each cycle. `GetActivated(threshold, limit)` returns traces above the threshold sorted by activation. This local cache is separate from Engram's server-side activation mechanics (also available via `BoostTraces` / `ReinforceTrace`).

8. **Memory quality evaluation**: `Judge.EvaluateSample(activityPath, sampleSize)` scans `activity.jsonl` for `memory_eval` events logged during sessions. Each entry contains a map of trace display IDs (first 5 chars) to self-ratings. The Judge resolves display IDs to full trace IDs via a live Engram lookup (`buildDisplayIDLookup`), fetches each trace's content, and calls `JudgeMemory(query, content)` using the Ollama LLM. It computes `SampleReport` statistics. This is triggered via the `memory_judge_sample` MCP tool.

## Design Decisions

- **Consolidation is fully delegated to Engram**: Bud2 never triggers consolidation explicitly. This keeps bud2's ingestion path simple (fire-and-forget) at the cost of having no control over consolidation timing. The `memory_flush` tool is a vestigial API surface from an earlier design where bud2 triggered consolidation.

- **Pre-computed embeddings are optional**: `IngestEpisodeRequest.Embedding` can be passed with the request to avoid a round-trip to Ollama on the Engram side. In practice, bud2's current ingestion paths don't pre-compute embeddings — Engram generates them. The `embedding.Client` in bud2 is used by the Judge and the local `TracePool` for similarity search.

- **FOLLOWS edges are always created**: Every consecutive pair of Discord messages in the same channel gets a `FOLLOWS` edge regardless of content. This encodes conversation order in the graph without requiring NLP. Edge confidence is hardcoded at 1.0.

- **Two separate trace stores**: The local `TracePool` (fast, in-process, ephemeral JSON) and Engram's persistent trace store serve different purposes. The local pool supports fast activation spreading and is used for context-building within a session. Engram is the authoritative long-term store. They are not automatically synchronized — the local pool is populated by tools that explicitly add to it, not by watching Engram.

- **Display ID correlation in the Judge**: Session self-evals log trace display IDs (first 5 chars of the trace ID, matching `GetOrAssignMemoryID()` in `simple_session.go`). The Judge must reverse-map these to full IDs via `buildDisplayIDLookup`, which calls `engram.Client.ListTraces()` at evaluation time. This is fragile if trace IDs are recycled, but adequate in practice because trace IDs are unique UUIDs.

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `cmd/bud` | `internal/engram` | Episode ingestion (`IngestEpisode`), trace search, unconsolidated ID fetch, FOLLOWS edge creation |
| `internal/executive` | `internal/engram` | `IngestEpisode` for subagent observations; `Search`, `GetUnconsolidatedEpisodeIDs`, `GetEpisodeSummariesBatch` for context assembly |
| `internal/mcp/tools` | `cmd/bud` (via callback) | `save_thought` → `AddThought` callback → `processInboxMessage` → `storeEpisode` |
| `internal/eval` | `internal/engram` | `ListTraces`, `GetTrace` for display ID resolution and trace content retrieval |
| `internal/eval` | `internal/embedding` | `JudgeMemory` uses `embedding.Client.Generate` for LLM-based rating |
| `internal/memory` (TracePool) | `internal/embedding` | `TracePool.SpreadActivation` uses `embedding.CosineSimilarity` for local activation |

## Non-Obvious Behaviors

- **`memory_flush` is a no-op**: Despite its name and description, the MCP tool does not flush anything. It logs a message and returns. Engram consolidates on its own schedule. If you're debugging missing consolidation, look at the Engram server, not this tool.

- **Subagent outputs are episodes**: When a subagent completes, `executive_v2.go` parses its JSON output for `observations` and `principles` arrays and ingests each item as a separate episode tagged `Source: "agent:<id>"`. These episodes enter the same consolidation pipeline as user messages.

- **Unconsolidated episode buffer is a safety net, not a primary path**: Episodes 31–100 in the conversation buffer are fetched only if they are unconsolidated (i.e., not yet linked to any trace). This prevents content from being lost between consolidation cycles but does not replace Engram's normal retrieval path — it supplements it.

- **TracePool and Engram are not synchronized**: Adding a trace to `TracePool` does not add it to Engram, and Engram consolidations don't automatically populate `TracePool`. The two stores diverge over time. `TracePool` is typically used for short-lived in-session state (e.g., tracking what was retrieved this session for activation spreading).

- **Judge reads append-only logs, not live state**: `EvaluateSample` scans `activity.jsonl` line by line using a buffered scanner with an enlarged buffer (for long lines). It does not use Engram's search API to find what was retrieved — it reconstructs the retrieval event from the log. This means the Judge's sample is bounded by what was logged, not what is in the DB.

- **Compression level 0 means raw stored summary**: `Level: 0` in a `Trace` does not mean "no compression" — it means the stored summary, which is already a condensed version of the source episodes. Levels 4/8/16/32 are further compressions to specific word counts. Requesting `level=0` from `GetTrace` returns the server's default stored form.

## Start Here

- `cmd/bud/main.go:~700` — `storeEpisode` closure: the canonical episode ingestion path; shows how Discord messages become Engram episodes with FOLLOWS edges
- `internal/engram/client.go` — all Engram API calls: `IngestEpisode`, `Search`, `GetUnconsolidatedEpisodeIDs`, `GetEpisodeSummariesBatch`, `BoostTraces`; the full protocol surface
- `internal/executive/executive_v2.go:~1207` — `buildConversationBuffer`: how retrieval works during context assembly; shows the unconsolidated-episode safety net
- `internal/memory/traces.go` — `TracePool`: local in-process trace cache with activation mechanics; distinct from Engram's store
- `internal/eval/judge.go` — `Judge.EvaluateSample`: independent memory quality evaluation; shows how self-ratings are correlated against an LLM judge
