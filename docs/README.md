# Bud Documentation

This directory contains design, architecture, and operational documentation for Bud.

## Directory Structure

```
docs/
├── architecture/                         # Current (v2) architecture and design
│   ├── v2-memory-architecture.md          # Canonical v2 design
│   └── message-flow.md                   # Message flow architecture
│
├── archive/                               # Historical and superseded docs
│   ├── architecture/                      # Archived architecture docs
│   ├── plans/                             # Executed implementation plans
│   ├── root/                              # Archived root-level docs
│   ├── v1/                                # V1 design (superseded by v2)
│   ├── improvement-ideas (obsolete).md
│   └── non-interactive (superseded).md
│
├── overview.md                            # Project overview
├── doc-plan.md                            # Documentation priority plan
├── testing-playbook.md                    # Manual test scenarios
│
├── attention-salience-computation.md      # Salience computation design
├── executive-text-capture.md              # Executive text capture design
├── external-integration-clients.md        # External integration clients
├── gtd-task-integration.md                # GTD task integration design
├── memory-consolidation-pipeline.md       # Memory consolidation pipeline
├── memory-quality-feedback-loop.md        # Memory quality feedback loop
├── mcp-tool-dispatch-registration.md      # MCP tool dispatch & registration
├── percept-ingestion-senses.md            # Percept ingestion & senses
├── plugin-manifest-runtime-tool-grants.md # Plugin manifest & tool grants
├── profiling.md                           # Profiling guide
├── reflex-evaluation-pipeline.md          # Reflex evaluation pipeline
├── seed-configuration-plugin-system.md    # Seed configuration & plugin system
├── session-lifecycle-context-assembly.md   # Session lifecycle & context assembly
├── skill-grants-agent-composition.md      # Skill grants & agent composition
├── startup-lifecycle-context-injection.md # Startup lifecycle & context injection
├── subagent-orchestration.md              # Subagent orchestration
├── things-integration.md                  # Things 3 integration
├── token-budget-session-caps.md           # Token budget & session caps
├── wake-scheduling-autonomous-sessions.md # Wake scheduling & autonomous sessions
└── zettel-library-discovery-generation.md # Zettel library discovery & generation
```

## Related Documentation

| Location | Purpose |
|----------|---------|
| `docs/architecture/` | How Bud is designed (for developers) |
| `state/system/guides/` | How Bud does things (for Bud's own reference) |
| `state/notes/` | Discovered knowledge and research (Bud's notes) |
| `seed/` | Default files seeded on first run |

## Quick Links

- **V2 Architecture**: `docs/architecture/v2-memory-architecture.md`
- **Guides for Bud**: `state/system/guides/` (GTD, integrations, reflexes, etc.)
- **Archived Plans**: `docs/archive/plans/`
