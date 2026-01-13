# Design Notes (V1 - Historical)

This directory contains **v1 design documents** from the initial Bud2 architecture (early January 2026).

## Current Documentation

The **v2 architecture** (implemented 2026-01-13) is documented in `docs/architecture/`:

| Document | Location | Description |
|----------|----------|-------------|
| **V2 Architecture** | `docs/architecture/v2-memory-architecture.md` | Full v2 design with implementation notes |
| **Research** | `docs/architecture/memory-research.md` | Academic research that informed v2 |
| **Test Plan** | `docs/architecture/test-plan.md` | V2 test scenarios |

## V2 Key Changes from V1

| Component | V1 | V2 |
|-----------|-----|-----|
| **Memory** | Flat traces (JSON) | Three-tier graph (SQLite): episodes → entities → traces |
| **Attention** | Thread-based routing | Focus-based single-stream with P0-P4 priorities |
| **Executive** | Multi-session per thread | Single persistent Claude session |
| **Context** | Full trace reload | Conversation buffer with incremental sync |
| **Retrieval** | Semantic similarity | Spreading activation + "feeling of knowing" |

## V1 Documents (Historical)

These files describe the original design and are kept for reference:

- `attention.md` - Thread-based attention (superseded by `internal/focus/`)
- `memory.md` - Flat trace architecture (superseded by `internal/graph/`)
- `executive.md` - Multi-session executive (superseded by `internal/executive/executive_v2.go`)
- `implementation-status.md` - V1 status (superseded)
- `autonomous-design.md` - Autonomous operation design
- `effectors.md` - Output system design
- `storage.md` - Persistence design
