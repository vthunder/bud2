# Memory Guide

How to decide where to save information so it's actually findable later.

## Storage Systems

| System | Role | Access |
|--------|------|--------|
| **Engram** | Passive ambient recall — surfaces relevant context without explicit query | Automatic injection; write via `save_thought` |
| **Zettels** | Primary knowledge store — atomic, linked, human-browsable ideas and findings | Explicit: `zettel-search`, `zettel-new` |
| **Notes/Guides** | Documents that must be read as a whole — multi-step guides, blog drafts, active plans | Read/Edit directly |
| **Things** | Task queue only — not for knowledge | `gtd_*` / Things MCP |

**Engram is for passive influence, not reliable recall.** Don't use it to store facts you'll need to look up — retrieval is probabilistic. Use zettels for that.

## Decision Tree

**Saving a passing observation, reasoning trace, or behavioral note?**
→ `save_thought` → Engram. Good for things you want to *influence future context* without explicit retrieval.

**Discovered a concept, insight, pattern, research finding, or anything worth preserving?**
→ `zettel-new` — creates an atomic note in `state/zettels/`. Run `zettel-search` first to avoid duplicates. This is the **default for new knowledge**.

**Learning something specific to a project (gotcha, decision, design context)?**
→ `zettel-new` with the project name as a tag (e.g. `#sandmill`). Search `zettel-search #sandmill` to find all cards for that project. For long-form coherent docs (design, API plan), use a named file in `state/projects/<project>/` (e.g. `design.md`).

**Writing something that must stay coherent as a whole (multi-step guide, blog draft, active plan)?**
→ Named file in `state/notes/` or `state/projects/<project>/`. Notes are for documents, not atoms.

## What NOT to Do

- Don't use Engram to store facts you'll need to look up — retrieval is probabilistic, not guaranteed.
- Don't use GK for general knowledge. It's for autopilot planning data only.
- Don't add facts to `MEMORY.md`. That file is a Claude Code artifact — it doesn't integrate with Bud's state system.
- Don't write ephemeral task details to any persistent file. Use Things for in-progress work.
- Don't duplicate content across multiple locations. Pick one canonical home.

## Memory Self-Eval (signal_done)

When calling `signal_done`, include `memory_eval` ratings for memories recalled during the session. The goal is to improve retrieval, not to be modest or generous.

| Rating | Meaning |
|--------|---------|
| 5 | Directly used — changed my approach or decision |
| 4 | Confirmed context I was already working from |
| 3 | Provided relevant background |
| 2 | Retrieved but didn't influence the work |
| 1 | Actively misleading or total noise |

**Calibration note (2026-02-22):** I was rating almost everything 1. External judge averaged 2.80. Greeting/social/operational traces that provide interaction pattern context → 3, not 1.
