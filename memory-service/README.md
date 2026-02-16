# Memory Service

A standalone HTTP service that provides a three-tier memory graph with spreading activation retrieval, extracted from [Bud's](https://github.com/vthunder/bud2) cognitive memory system.

## Architecture

The memory system implements a three-tier knowledge graph:

| Tier | Name | Description |
|------|------|-------------|
| 1 | **Episodes** | Raw messages with full metadata, reply chains, dialogue acts, embeddings |
| 2 | **Entities** | Named entities (people, orgs, places) with aliases, relationships, salience |
| 3 | **Traces** | Consolidated memories with pyramid summaries, spreading activation |

**Retrieval** uses Synapse-style spreading activation with dual-trigger seeding (semantic + lexical + entity-based), lateral inhibition, sigmoid transform, and "feeling of knowing" rejection.

## API

### `GET /health`

Returns service status and graph statistics.

### `POST /ingest`

Store an episode, run NER, extract entities, resolve against existing graph.

```json
{
  "content": "Sarah from Google mentioned the Portland office is closing",
  "source": "slack",
  "author": "Dan",
  "author_id": "U123",
  "channel": "general",
  "reply_to": "ep-...",
  "timestamp": "2026-02-16T12:00:00Z"
}
```

**Response:**
```json
{
  "episode_id": "ep-1739...",
  "short_id": "a3f9c",
  "entities": [
    {"id": "entity-abc", "name": "Sarah", "type": "PERSON", "is_new": true, "matched_by": "created"},
    {"id": "entity-def", "name": "Google", "type": "ORG", "is_new": false, "matched_by": "exact"}
  ],
  "entropy_score": 0.72
}
```

### `POST /recall`

Spreading activation retrieval given a natural language query.

```json
{
  "query": "What do we know about Sarah?",
  "limit": 10,
  "context_ids": ["trace-abc"]
}
```

**Response:**
```json
{
  "traces": [
    {
      "id": "trace-123",
      "short_id": "b2e4f",
      "summary": "Sarah from Google mentioned the Portland office is closing.",
      "topic": "conversation",
      "trace_type": "knowledge",
      "activation": 0.87,
      "strength": 3,
      "created_at": "2026-02-16T12:01:00Z"
    }
  ]
}
```

### `POST /context`

Assemble a context window with pyramid-compressed recent conversation and optionally retrieved memories.

```json
{
  "channel": "general",
  "query": "Sarah at Google",
  "max_episodes": 30,
  "max_traces": 10
}
```

**Response:**
```json
{
  "recent_conversation": "[a3f9c] [12:00] Dan: Sarah from Google mentioned...\n...",
  "retrieved_memories": [...],
  "stats": {"episode_count": 30, "trace_count": 5}
}
```

## External Dependencies

| Dependency | Required | Purpose |
|-----------|----------|---------|
| **Ollama** | Yes | Embedding generation (`nomic-embed-text`) and text generation (`llama3.2`) for compression/summarization |
| **spaCy NER sidecar** | No (recommended) | Fast entity pre-filtering (~10ms). Falls back to running deep extraction on all messages if unavailable |
| **SQLite** | Embedded | Graph database storage (via `go-sqlite3`) |

### Ollama Setup

```bash
# Install Ollama: https://ollama.ai
ollama pull nomic-embed-text
ollama pull llama3.2
```

### spaCy NER Sidecar (optional)

The sidecar is a simple Flask app running spaCy's `en_core_web_sm` model:

```bash
pip install spacy flask
python -m spacy download en_core_web_sm
# Run sidecar/server.py from the bud2 repo
```

## Configuration

All configuration via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `MEMORY_PORT` | `8230` | HTTP listen port |
| `MEMORY_DATA_DIR` | `./data` | Directory for SQLite database |
| `OLLAMA_URL` | `http://localhost:11434` | Ollama API URL |
| `OLLAMA_EMBED_MODEL` | `nomic-embed-text` | Embedding model |
| `OLLAMA_GEN_MODEL` | `llama3.2` | Generation model |
| `NER_SIDECAR_URL` | `http://localhost:5100` | spaCy NER sidecar URL |

## Running

```bash
cd memory-service
go build -o memory-service ./cmd/memory-service
./memory-service
```

## Key Algorithms

- **Spreading Activation** (Synapse paper): 3-iteration activation spread with decay, fan-out normalization, lateral inhibition (top-7 winners suppress), and sigmoid transform
- **Dual-Trigger Seeding**: Union of semantic (embedding cosine similarity), lexical (keyword BM25-style), and entity-based seed nodes
- **Pyramid Summarization**: Episodes compressed to L4/L8/L16/L32/L64 word targets for adaptive context windows
- **Entity Resolution**: Exact match → name substring → first name match → embedding similarity → create new
- **Feeling of Knowing**: Queries with max activation < 0.12 are rejected (low confidence)

## Package Structure

```
pkg/
├── graph/          # Core: episodes, entities, traces, activation, retrieval, compression
├── embedding/      # Ollama client for embeddings and text generation
├── ner/            # spaCy NER sidecar client
├── extract/        # Entity extraction (fast regex + deep LLM), resolution, invalidation
├── filter/         # Quality filtering (entropy, dialogue act classification)
└── consolidate/    # Trace consolidation (episode grouping, summarization, edge inference)
```
