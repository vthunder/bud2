---
notion_id: 2ede67c666dd8107857ff70bbfd36d40
title: Avail-Style Projects
---

# Avail-Style Projects

This guide describes how we work on Avail projects - team-collaborative projects with shared documentation accessible to colleagues and their LLMs.

## Core Principles

1. **Documents are for decisions, not instructions** - AI doesn't need verbose specs to implement
2. **Generate docs from artifacts, not just artifacts from docs** - code can be source of truth
3. **Parallel exploration beats sequential handoffs** - run ahead and prototype
4. **Record *why*, not just *what*** - future AI/humans need decision context
5. **Acceptance criteria over detailed specs** - define success, not implementation

## Project Structure

Avail-style projects have docs in a shared repo, separate from Bud's internal state:

```
~/src/project-docs/           # Shared repo (pushed to Avail GitHub)
├── nightshade/
│   ├── README.md           # Main dashboard (Notion-synced)
│   ├── acceptance/           # Success criteria per milestone
│   ├── decisions/            # Architecture Decision Records
│   ├── explorations/         # Research & spikes
│   └── ...
├── other-project/
└── common/                   # Shared utilities, guidelines

state/projects/avail/nightshade/   # Bud's internal state (not shared)
├── notes.md                       # Pointer + internal notes
└── (other internal files)
```

### Three-Way Presence

1. **~/src/project-docs/** - Working copy for LLM context, shared with colleagues
2. **Avail GitHub** - Published version (push from project-docs when ready)
3. **Notion** - Synced docs for team collaboration

## Identifying Avail-Style Projects

Projects are marked in their `notes.md` frontmatter:

```yaml
---
type: avail-style
docs: ~/src/project-docs/nightshade/
---
# Project Notes

(internal notes here)
```

When Bud loads a project and sees `type: avail-style`, load this guide and read docs from the `docs:` path.

## Key Files

### README.md (in project-docs)

The main project dashboard. This syncs with the primary Notion page.

**Contains:**
- Current status and blockers
- Team/ownership
- Links to all other docs
- Key decision log
- Changelog

**Has frontmatter with `notion_id` for sync.**

### notes.md (in state/projects/)

Bud's internal notes - NOT shared. Contains:
- Pointer to docs folder (in frontmatter)
- Internal observations
- Research in progress
- Links for quick reference

### Other docs

| Type | Purpose | Location |
|------|---------|----------|
| Acceptance criteria | Define success per milestone | `acceptance/` |
| Decisions (ADRs) | Architectural choices with rationale | `decisions/` |
| Explorations | Research, spikes, prototypes | `explorations/` |
| Generated docs | Auto-generated from code | `generated/` |

## Notion Sync Workflow

**Before making changes:**
```bash
# Pull latest from Notion to specific file (captures team edits)
notion_pull(page_id="<page_id>", output_path="~/src/project-docs/<project>/README.md")
```

**After making changes:**
```bash
# Push back to Notion
notion_push(file_path="~/src/project-docs/<project>/README.md")
```

**Conflict handling:** For now, pull before push and avoid simultaneous edits. If conflicts occur, the push will overwrite Notion.

## Git Workflow

The `project-docs` repo is pushed to Avail GitHub for colleague access:

```bash
cd ~/src/project-docs
git add .
git commit -m "Update nightshade docs"
git push origin main
```

Colleagues can clone the repo and use their LLMs with the docs as context.

## Document Types

### Acceptance Criteria

Define what success looks like. These are the "tests" that determine done.

```markdown
# M1: Core Privacy Flow

## Must Have
- [ ] User can deposit ETH via MetaMask
- [ ] Shielded balance displays within 60 seconds
- [ ] User can withdraw to any address

## Success Metrics
- Deposit → balance → withdraw works end-to-end
- No user-visible errors in happy path

## Out of Scope
- Real ZK proofs (mock is fine)
- Mobile support
```

### Decisions (ADRs)

Capture architectural/design decisions with context.

```markdown
# ADR-001: Use Commitment-Based Privacy

## Status
Accepted

## Context
Need to hide transaction details while allowing verification.

## Options Considered
1. Commitment scheme - hash(note) published, note kept private
2. Stealth addresses only - one-time addresses, no shielded pool
3. MPC-based - multi-party computation

## Decision
Option 1: Commitment scheme (Zcash-style)

## Rationale
- Proven approach (Tornado Cash, Zcash)
- Simpler than MPC
- Stealth addresses alone don't hide amounts

## Consequences
- Need nullifiers to prevent double-spend
- Need Merkle tree for inclusion proofs
```

### Explorations

Document prototypes, spikes, and research.

**Contains:**
- Problem being explored
- Approaches tried
- Findings/learnings
- Recommendation (if any)

## Workflows

### Starting a New Avail-Style Project

1. Create folder in project-docs: `~/src/project-docs/<project>/`
2. Create `README.md` with initial content
3. Create Notion page and add `notion_id` to frontmatter
4. Create internal folder: `state/projects/avail/<project>/`
5. Create `notes.md` with frontmatter pointing to docs folder

### Resuming Work on a Project

1. Check `notes.md` for the `docs:` path
2. Load this guide (avail-style-projects.md)
3. Pull latest from Notion: `notion_pull`
4. Read `README.md` and relevant docs
5. Work on the project
6. Push changes to Notion when done

### Starting a Feature

1. Human writes acceptance criteria in `acceptance/`
2. AI explores approaches (may generate multiple prototypes)
3. Human chooses direction
4. AI implements with human review
5. Decision captured as ADR if significant
6. Dashboard updated with status

## Roles

| Role | Primary Responsibility |
|------|----------------------|
| Product | Define acceptance criteria, prioritize, choose between options |
| Engineering | Review AI output, make architectural decisions |
| Design | Define UX constraints, review generated UI |
| AI | Explore options, implement quickly, maintain docs |

**Key shift:** Humans focus on *decisions* and *quality*. AI handles *volume* and *speed*.

## Anti-Patterns

- Writing detailed specs before any exploration
- Waiting for approval before prototyping
- Manual maintenance of generated docs
- Decisions made without recording rationale
- Treating AI output as final without review

## Related

- [Projects Guide](./projects.md) - General project folder structure
- [Repositories Guide](./repositories.md) - Code repository workflows
