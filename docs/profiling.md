# Message Processing Profiler

The profiler provides detailed timing measurements for Bud's message processing pipeline.

## Usage

Enable profiling by setting the `BUD_PROFILE` environment variable:

```bash
# Minimal profiling (L1: key stages only)
export BUD_PROFILE=minimal

# Detailed profiling (L2: includes substages) - recommended
export BUD_PROFILE=detailed

# Trace profiling (L3: every function) - future implementation
export BUD_PROFILE=trace
```

Profiling logs are written to `state/system/profiling.jsonl` in JSON Lines format.

## Profiling Levels

### L1: Minimal (Production-Safe)
Overhead: <1ms per message

Stages measured:
- `processInboxMessage` - Overall message handling
- `ingest.total` - Total ingestion time
- `percept.total` - Total percept processing
- `executive.total` - Total executive processing
- `executive.claude_api` - Claude API call duration

### L2: Detailed (Debug Mode) **← Recommended**
Overhead: ~5ms per message

Additional substages:
- **Ingest**:
  - `ingest.episode_store` - Episode storage
  - `ingest.ner_check` - NER pre-check
  - `ingest.entity_extract` - Ollama entity extraction
  - `ingest.summary_gen` - Episode summary generation
- **Percept**:
  - `percept.reflex_check` - Reflex processing
  - `percept.queue_add` - Focus queue addition
- **Executive**:
  - `executive.context_build` - Context assembly
  - `executive.prompt_build` - Prompt generation
  - `context.conversation_load` - Recent conversation retrieval
  - `context.memory_retrieval` - Memory graph query

## Output Format

Each line in `profiling.jsonl` is a JSON object:

```json
{
  "message_id": "discord-1234567890-9876543210",
  "stage": "ingest.entity_extract",
  "start_time": "2026-02-16T09:30:00.123Z",
  "duration_ms": 620000000,
  "metadata": {}
}
```

## Analysis

### View recent profiling data
```bash
tail -100 state/system/profiling.jsonl | jq -s '
  group_by(.stage) |
  map({
    stage: .[0].stage,
    count: length,
    avg_ms: (map(.duration_ms / 1000000) | add / length),
    max_ms: (map(.duration_ms / 1000000) | max)
  }) |
  sort_by(.avg_ms) |
  reverse
'
```

### Find slowest operations
```bash
cat state/system/profiling.jsonl | jq -s 'sort_by(.duration_ms) | reverse | .[0:20]'
```

### Per-message breakdown
```bash
MESSAGE_ID="discord-1234567890-9876543210"
cat state/system/profiling.jsonl | jq -s --arg id "$MESSAGE_ID" '
  map(select(.message_id == $id)) |
  sort_by(.start_time) |
  map({stage, duration_ms: (.duration_ms / 1000000)})
'
```

## Key Metrics

Typical timings for a user message with entity extraction:

| Stage | Expected Duration |
|-------|------------------|
| `ingest.episode_store` | 10-20ms |
| `ingest.ner_check` | 5-15ms |
| `ingest.entity_extract` | 500-800ms (Ollama) |
| `ingest.summary_gen` | 150-300ms (Ollama) |
| `percept.reflex_check` | 30-50ms |
| `context.conversation_load` | 10-20ms |
| `context.memory_retrieval` | 100-200ms |
| `executive.claude_api` | 2000-5000ms |

**Total end-to-end**: 4-6 seconds for complex messages

## Performance Targets

- **Episode storage**: <50ms
- **NER check**: <20ms
- **Context assembly**: <300ms total
- **Claude API**: Variable (depends on response length)

## Troubleshooting

### Profiler not logging
1. Check `BUD_PROFILE` is set: `echo $BUD_PROFILE`
2. Verify profiling.jsonl exists: `ls -la state/system/profiling.jsonl`
3. Check file permissions: `chmod 644 state/system/profiling.jsonl`

### High overhead
- Use `minimal` level for production
- `detailed` level adds ~5ms per message (acceptable for debugging)

### Missing stages
- Some stages only run conditionally:
  - `ingest.entity_extract` skipped if NER finds no entities
  - `context.memory_retrieval` skipped for autonomous wakes
