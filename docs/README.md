# Bud Documentation

This directory contains design, architecture, and operational documentation for Bud.

## Directory Structure

```
docs/
├── architecture/          # Current (v2) architecture and design
│   ├── v2-memory-architecture.md   # Canonical v2 design
│   ├── memory-research.md          # Research that informed v2
│   └── test-plan.md                # V2 test scenarios
│
├── v1/                    # Historical v1 design (superseded)
│   ├── README.md          # V1 → V2 migration notes
│   ├── memory.md          # V1 memory architecture
│   ├── attention.md       # V1 thread-based attention
│   └── executive.md       # V1 multi-session executive
│
├── plans/                 # Implementation plans (historical records)
│   └── YYYY-MM-DD-*.md    # Dated implementation plans
│
├── testing-playbook.md    # Manual test scenarios
└── testing-notion.md      # Notion integration testing
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
- **Implementation Plans**: `docs/plans/`
