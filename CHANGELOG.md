# Changelog

## [v0.1.0] - 2026-02-21

First milestone release. Bud is a working personal AI agent: it reads Discord, thinks, remembers, and replies — continuously and autonomously, deployed on a Mac Mini.

### Core Architecture

- **Sense → Attention → Executive pipeline**: Discord messages are ingested as percepts, routed through an attention loop, and processed by an executive (Claude Code) that thinks and responds
- **Event-driven message processing**: Parallel P1 (user messages) and background (autonomous) processing with priority queue
- **One-shot session model**: Each executive wake is a fresh Claude Code session with full memory context injected
- **Token-based session reset**: Sessions reset after output token budget exceeded, preventing context overflow
- **Discord effector**: Sends responses to Discord, supports typing indicators, emoji reactions, and slash commands

### Memory System

- **Graph database** (`internal/graph`): SQLite-backed storage for episodes, traces, entities, and relations
- **Episodes**: Raw conversation turns captured with speaker, channel, content, and timestamps
- **Traces**: Consolidated memory units with pyramid summaries (L1–L5 compression levels) and activation scores
- **Entity extraction**: Entities and subject-predicate-object relationship triples extracted from episodes via spaCy NER sidecar
- **Entity resolution**: Fuzzy matching + embedding-based deduplication to prevent duplicate entity creation
- **Temporal edges**: FOLLOWS edges between adjacent episodes; SIMILAR_TO edges between related traces
- **Memory consolidation**: Batch processing of unconsolidated episodes into traces with compression
- **Smart consolidation trigger**: Variable buffer threshold with sqlite-vec and FTS5 indexes updated on consolidation
- **Activation decay**: Recency-weighted retrieval; activation decays over time and spreads through graph edges
- **Two-phase funnel retrieval**: Semantic + lexical + entity triggers feed spreading activation; top candidates promoted to full detail
- **Operational trace demotion**: Meeting reminders and status queries deprioritized in retrieval
- **Invalidation detection**: Old entity relations marked superseded when contradictory facts are ingested

### Engram Migration (complete — awaiting activation)

- **`internal/engram` HTTP client**: New package providing typed Go client for the Engram memory service
- **Phase 1 complete**: `executive_v2.go` now uses Engram HTTP client instead of direct `graph.DB` calls
- **Phase 2 complete**: MCP tools (`list_traces`, `search_memory`, `get_trace_context`, `query_trace`, `query_episode`) migrated to HTTP client
- **Phase 3+4 complete**: Episode ingestion (`ingestToMemoryGraph`, `captureResponse`) and consolidation trigger/decay migrated to Engram HTTP client; entity relation extraction removed (Option D)
- **Phase 5 complete**: `state_traces` and `memory_judge_sample` migrated to Engram; `internal/graph` removed from `cmd/bud` transitive dep tree
- **New Engram endpoints**: Seven new API routes added to cover executive retrieval, episode ingestion, and state management
- **Decay/consolidation goroutines removed**: Engram service now owns consolidation and activation decay; bud2 no longer runs these background loops
- **`cmd/migrate-episodes`**: One-time migration tool to replay all historical SQLite episodes into Engram (run once after activation)
- **Deployment config**: launchd plist created at `~/Library/LaunchAgents/com.bud.engram.plist` (ready to activate)

### MCP Tools

- **`save_thought`**: Persist observations to memory graph directly
- **`talk_to_user`**: Send messages to Discord (primary communication channel)
- **`discord_react`**: React to Discord messages with emoji
- **`signal_done`**: Track thinking time and enable autonomous scheduling
- **Things 3 integration**: Full GTD task management via MCP (add, list, update, complete todos/projects/areas)
- **Google Calendar integration**: Today's events, upcoming events, free/busy, create events
- **Notion integration**: Pull/push/diff documents via efficient-notion-mcp
- **State introspection tools**: `state_summary`, `state_traces`, `state_percepts`, `state_threads`, `state_logs`, `state_queues`
- **Memory eval tools**: `memory_judge_sample` for evaluating memory retrieval quality
- **Reflex management**: `create_reflex`, `list_reflexes`, `delete_reflex`
- **GTD tools**: `gtd_add`, `gtd_list`, `gtd_complete`, `gtd_update`, `gtd_areas`, `gtd_projects`

### Reflex System

- **Pattern-matched automated responses**: Reflexes handle simple queries without waking the full executive
- **Hot-reload**: Reflex YAML files watched for changes; updates apply without restart
- **Reflex actions**: `reply`, `call_tool`, `json_query`, `invoke_reflex`
- **Seeded reflexes**: GTD queries, meeting reminders, state-sync, and other common patterns shipped as seed files
- **Git state-sync reflex**: Automatically commits and pushes state/ on a schedule

### Autonomous Operation

- **30-minute autonomous wakes**: Periodic executive wakes to check tasks, do background work
- **Quiet hours**: Wakes suppressed 23:00–07:00 (Europe/Berlin) to avoid overnight resource waste
- **Wellness checklist**: Daily housekeeping routine (memory introspection, index updates, task review)
- **Self-reflection**: Wake includes reviewing recent activity and commitments
- **Startup notification**: Discord message sent when daemon restarts
- **Calendar reminders**: Upcoming events surfaced via autonomous wake or calendar MCP

### Performance Optimizations

- **Embedding cache**: 256-entry in-memory FIFO cache on Ollama `Embed()` calls
- **Embedding prefetch**: On message arrival, embedding starts before executive wakes (cuts critical-path latency)
- **Conversation load bottleneck fixed**: `GetRecentEpisodes` no longer fetches the `embedding` column (400ms → 34ms)
- **N+1 eliminated**: Episode summaries batch-fetched in `buildRecentConversation`
- **Entity list cache**: Pre-compiled regex patterns cached to avoid repeated DB queries
- **Entity embedding cache**: Omit entity embeddings from cache rebuild query
- **Sub-operation profiling**: Fine-grained timing across memory retrieval pipeline (embedding, triggers, spread, funnel)
- **FTS5 + sqlite-vec indexes**: Lexical and semantic retrieval via indexed virtual tables
- **Batch neighbor loading**: Spreading activation uses 2 queries for N nodes (vs N+1)
- **Disabled memory retrieval for autonomous wakes**: Saves ~400ms per idle wake

### Entity & Relationship Extraction

- **spaCy NER sidecar**: Replaced entropy-filter heuristics with spaCy Named Entity Recognition
- **Relationship extraction**: Subject-predicate-object triples extracted from conversations via Ollama
- **Entity deduplication**: Case-insensitive lookup + alias matching
- **Bud response ingestion**: Bud's own responses captured and entity-extracted (not just user messages)
- **PRODUCT false-positive filtering**: Reduced noise in extracted entities

### Deployment

- **launchd service**: `com.bud.daemon` plist for persistent Mac Mini deployment
- **Log routing**: stdout/stderr to `~/Library/Logs/bud.log`
- **`.env` support**: Environment variables loaded from `bud2/.env`
- **NER sidecar plist**: `com.bud.ner` launchd service for spaCy entity extraction
- **Engram sidecar plist**: `com.bud.engram` launchd service (ready to activate)
- **Non-interactive mode**: Auto-kill old process when launched by launchd
- **Discord connection health monitoring**: Detects stale connections, triggers hard reset

### State Management

- **`state/` directory structure**: Organized into `system/`, `notes/`, `projects/`, `queues/`
- **Activity log** (`activity.jsonl`): All inputs, decisions, actions, and executive wake/done events
- **Journal**: Structured decision/action logging with reasoning and outcomes
- **Percepts**: Short-term memory for incoming messages
- **Threads**: Working memory for in-progress tasks
- **Auto-memory**: `MEMORY.md` and topic files in `state/memory/` persist across conversations

### Testing

- **E2E memory pipeline test suite**: 10 scenarios covering retrieval, decay, operational bias, entity resolution
- **Memory retrieval evaluation framework**: LLM-judge ratings vs self-eval to detect bias
- **Graph unit tests**: Reconsolidation, episode-trace edges, entity resolution, consolidation

---

## What's Next (v0.2.0)

- **Activate Engram**: Load `com.bud.engram.plist`, set `ENGRAM_URL`+`ENGRAM_API_KEY` in `.env`, restart bud2
- **Run migrate-episodes**: One-time replay of 1735 SQLite episodes into Engram after activation
- **Remove remaining graph packages**: Once Engram is running and data migrated, `internal/graph`, `internal/embedding`, `internal/consolidate` can be removed (blocked on `stateInspector` + `memoryJudge` Phase 5 migration)
- **Adaptive wake frequency**: Dynamic wake intervals based on activity level
- **Embedding speed**: Faster embedding model now that Engram owns the embedding path
