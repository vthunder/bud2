---
name: repo-doc
description: "Generate or refresh overview.md and doc-plan.md for a code repository. Trigger: 'repo-doc', 'generate overview', 'document this repo', 'refresh repo docs'."
user-invocable: true
---

# repo-doc

Generate or refresh `overview.md` and `doc-plan.md` for a code repository.

## Flags

- `--autonomous` — non-interactive mode. Implies: auto-commit (no prompt), default to `maintained` if no docs exist yet, proceed even if docs are current (regenerate). Intended for autonomous wakes and the `dev:doc-maintain` meta-skill.

## Steps

### 1. Find the repo

Determine `repo_name` (short name, e.g. `bud2`) and `repo_path` (default `~/src/<repo_name>` unless the user specifies otherwise). If the checkout does not exist at `repo_path`, stop and tell the user.

**Locate existing docs — check in order:**
1. `~/src/<repo_name>/docs/overview.md` → if found, type is `maintained`
2. `state/projects/<repo_name>/overview.md` → if found, type is `reference`
3. If **neither exists**:
   - If `--autonomous`: default to `maintained` (`doc_dir = ~/src/<repo_name>/docs/`). No prompt.
   - Otherwise: ask the user where to write the docs:
     > "No docs found for `<repo_name>`. Where should I write them?
     > - `~/src/<repo_name>/docs/` — maintained (docs committed to the repo)
     > - `state/projects/<repo_name>/` — reference (local only, never committed)"

   Set `repo_type` from the user's answer.

Determine:
- `repo_type` — `maintained` or `reference`
- `doc_dir` — where overview.md and doc-meta.json will be written:
  - maintained → `~/src/<repo_name>/docs/`
  - reference → `state/projects/<repo_name>/`

### 2. Staleness check

Read `<doc_dir>/doc-meta.json` if it exists. It has the form:
```json
{"docs_commit": "<SHA>", "generated_at": "<ISO>", "repomix_version": "<version>"}
```

**Check whether docs are current:**
```bash
# Any code commits after docs_commit that aren't themselves docs commits?
git -C <repo_path> log <docs_commit>..HEAD --oneline -- ':!docs/'
```

- If the command returns **empty output**: docs are current.
  - If `--autonomous`: proceed anyway (regenerate silently).
  - Otherwise: tell the user and offer to force-regenerate (`--force`). Stop unless forced.
- If the command returns commits (or `doc-meta.json` doesn't exist): proceed with regeneration.

**Capture the current HEAD SHA before any writes:**
```bash
CURRENT_SHA=$(git -C <repo_path> rev-parse HEAD)
```

Store this — it becomes `docs_commit` in the output meta. Do **not** recapture it after writing.

### 3. Run extraction

Run the extraction script in a Bash subprocess:
```bash
bash state/system/plugins/dev/skills/repo-doc/extract.sh <repo_path> /tmp/repo-doc-<repo_name>/
```

The script collects:
- `/tmp/repo-doc-<repo_name>/compressed.md` — repomix compressed output (structure + signatures)
- `/tmp/repo-doc-<repo_name>/tree.md` — repomix file tree only
- `/tmp/repo-doc-<repo_name>/readme.md` — README content (if found)
- `/tmp/repo-doc-<repo_name>/manifest.md` — package.json / go.mod / composer.json (whichever found)
- `/tmp/repo-doc-<repo_name>/summary.txt` — sizes and token estimates

If the script exits with code 1 (repomix not installed), note the limitation and proceed with the fallback output that the script provides.

For **maintained repos only**: also read `state/projects/<repo_name>/notes.md` if it exists — this provides architectural context not in the code.

### 4. Synthesize overview.md

Feed the extracted content into the synthesis prompt below. Generate the `overview.md` content.

---

## Synthesis Prompt

You are generating an `overview.md` for a code repository. This document is used by engineers (including AI agents) to orient quickly when starting a new task.

**Inputs provided:**
- Compressed repo map (structure + symbol signatures): `compressed.md`
- File tree: `tree.md`
- README: `readme.md`
- Package manifest: `manifest.md`
- Project notes (maintained repos only, may be absent): `notes.md`

**Instructions:**

1. **Purpose section** (2 sentences max): What this repo does and why it exists. Include the primary tech stack inline (e.g., "Built in Go", "React + TypeScript frontend"). Do not pad — if two sentences are enough, stop.

2. **Data Flow section** (1–2 paragraphs): Trace how a request, event, or operation flows through the system. Name the actual key components in sequence (file paths or package names, not abstract categories). If you are inferring from structure rather than explicit flow code, write "likely flows through X" — do not fabricate certainty. For repos with no clear request flow (CLI tools, libraries, config repos), describe the primary usage pattern instead.

3. **Module Map table**: List the 10–15 most important paths only. Skip test utilities, generated files, and one-off scripts unless they are central to understanding the repo. Each row: `path` | one-line description of what this module owns.

4. **Key Files**: 5–10 specific files that a new engineer should read first. One-liner per file. Prefer entry points, core data structures, and configuration.

5. **Conventions section**: Extract from the code, not from generic best practices. If you cannot determine the testing approach from the code, write "unknown — check README". Same for naming. Be honest about gaps.

6. **Start Here section**: Make this actionable — name actual file paths, not vague categories like "look at the models". If uncertain, give your best guess and mark it `(inferred)`.

7. If project notes were provided: incorporate known architectural decisions, tradeoffs, or gotchas from those notes into the relevant sections. Do not create a separate "Project Notes" section — blend them in.

8. Add a frontmatter block at the top:
```yaml
---
generated_at: <ISO timestamp>
commit: <short SHA — first 8 chars of docs_commit>
repomix: <"available" or "unavailable — used fallback">
---
```

**Output format** (follow exactly):

```markdown
---
generated_at: <ISO>
commit: <short-sha>
repomix: <available|unavailable — used fallback>
---

# <Repo Name> — Overview

> Generated: <date> | Commit: <short SHA>

## Purpose

<2 sentences max>

## Data Flow

<Narrative paragraph(s). Name actual components. Mark inferences with "likely".>

## Module Map

| Path | Responsibility |
|------|---------------|
| ... | ... |

## Key Files

- `path/to/file` — one-liner
- ...

## Conventions

- **Testing**: ...
- **Naming**: ...
- **Entry points**: ...
- **Patterns to know**: ...

## Start Here

For a given task type, start at:
- **Adding a feature**: `path/to/relevant/file` — why
- **Fixing a bug**: `path/to/relevant/file` — why
- **Understanding the data model**: `path/to/relevant/file` — why
- **Running locally**: `path/to/relevant/file` — why
```

---

### 4b. Synthesize doc-plan.md

After writing `overview.md`, generate a ranked topic list for architectural deep-dives.

Doc-plan topics are **architectural concepts, not module paths**. A topic like "session lifecycle" or "memory consolidation pipeline" may span several modules. The module scoring data is evidence used to discover and rank topics — not the topic list itself.

**Gather inputs:**
1. Read `<tmp_dir>/scoring-data.md` from the extraction step
2. Check what arch docs already exist: list `<doc_dir>/*.md` excluding `overview.md` and `doc-plan.md`. These are "existing docs". Note which ones have a `commit:` frontmatter field and compute approximate age from it.
3. The full text of the just-generated `overview.md`

Feed these into the two-phase scoring prompt below and generate `doc-plan.md`.

---

## Doc-Plan Prompt (two phases)

### Phase 1 — Topic ideation

You are generating candidate topics for architectural deep-dives in a code repository.

**Inputs provided:**
- `overview.md` — structural overview (purpose, data flow, module map)
- `scoring-data.md` — per-module metrics (files, LoC, commits/90d, fix-commits/90d, centrality)

**Your task:** Generate 12–20 candidate architectural topics. Topics are:
- Named as concepts or processes, not file paths (e.g. "Session Lifecycle", "Wake Scheduling Loop", "Memory Consolidation Pipeline")
- Cross-cutting: a topic may span multiple modules — list the key modules it touches
- Diverse: include request flows, data lifecycles, subsystem internals, integration seams, and cross-cutting concerns

For each candidate topic output a line:
```
<Topic Name> | modules: <comma-separated paths> | rationale: <one sentence why this is architecturally interesting>
```

Do not score yet. Just enumerate.

---

### Phase 2 — Topic scoring and ranking

Given the candidate topics from Phase 1, score and rank them.

**Scoring formula:**
`score = centrality(0.30) + coverage_gap(0.30) + complexity(0.20) + churn(0.10) + bug_density(0.10)`

For a topic that spans multiple modules, use the **maximum** value of each signal across its constituent modules (a topic inherits the highest signal from any module it touches).

All raw signals are normalized 0–1 relative to all modules in scoring-data.md before applying weights.

**Signal definitions:**
- **centrality**: max `Centrality (import refs)` across topic's modules, normalized
- **coverage_gap**: `1.0` if no arch doc exists for this topic, `0.5` if a related doc exists but its commit is >90 days old, `0.0` if fresh
- **complexity**: max `(files + loc/100)` across topic's modules, normalized
- **churn**: max `Commits 90d` across topic's modules, normalized
- **bug_density**: max `Fix-Commits 90d` across topic's modules, normalized

**Qualitative override:** If a topic is a foundational cross-cutting concern (auth, error handling, scheduling, serialization, configuration) OR its top constituent module is in the top quartile for both centrality and complexity, boost score by 0.10 (cap 1.0). Label `(foundational)` in Signals column.

**Output format (write exactly this):**

```markdown
# Doc Plan: <repo> — <YYYY-MM-DD>

Scoring: centrality (0.30) + coverage gap (0.30) + complexity (0.20) + churn (0.10) + bug density (0.10)
Topics span modules — signals are the max across constituent modules.

| Rank | Topic | Score | Key Modules | Signals | Status |
|------|-------|-------|-------------|---------|--------|
| 1 | Session Lifecycle | 0.87 | `internal/executive`, `internal/types` | centrality 28, no doc, 112 commits/90d | missing |
| 2 | Memory Consolidation Pipeline | 0.74 | `internal/engram`, `internal/mcp` | centrality 14, doc stale ~180d, 12 fix-commits | stale |
| 3 | MCP Tool Dispatch | 0.61 | `internal/mcp`, `internal/integrations` | centrality 16, no doc, low churn | missing |

## Recommended next

Run `dev:arch-doc "<top-ranked topic>"` on `<repo>` — <one sentence why>.

---
_Generated: <ISO> | Commit: <short SHA>_
```

Include 8–15 topics after scoring. Drop topics scoring below 0.25.

---

### 5. Write outputs

Ensure `<doc_dir>` exists (create it if needed).

Write `<doc_dir>/overview.md` with the synthesized content.

Write `<doc_dir>/doc-plan.md` with the scored topic list from step 4b.

Write `<doc_dir>/doc-meta.json`:
```json
{
  "docs_commit": "<CURRENT_SHA captured in step 2>",
  "generated_at": "<ISO timestamp>",
  "repomix_version": "<version from extract output, or 'unavailable'>"
}
```

**Important**: use the SHA captured in step 2 — never re-run `git rev-parse HEAD` here, as a prior commit step could have advanced HEAD.

### 6. Commit (maintained repos only)

For **maintained repos** after writing:
- If `--autonomous`: run the commit directly without prompting.
- Otherwise, prompt:
  ```
  Docs written to <doc_dir>/overview.md and <doc_dir>/doc-plan.md

  Run to commit:
    git -C <repo_path> add docs/ && git -C <repo_path> commit -m 'docs: regenerate overview and doc plan'

  Commit now? [y/n]
  ```
  If the user confirms, run the commit. If they decline, leave the files untracked.

Commit command (used in both paths):
```bash
git -C <repo_path> add docs/ && git -C <repo_path> commit -m 'docs: regenerate overview and doc plan'
```

For **reference repos**: never prompt to commit, never run git commit.

### 7. Summary

Tell the user what was produced:

```
Generated docs for `<repo_name>`:
- overview.md — structural overview (<N> modules mapped)
- doc-plan.md — <N> areas ranked by documentation priority

Top arch-doc candidate: `<top area>` (score <X>)
Run `dev:arch-doc <topic>` to generate a deep-dive, or `dev:doc-audit` to scan existing docs.
```

---

## Staleness Check Reference

```bash
# Is the repo stale relative to last doc generation?
git -C <repo_path> log <docs_commit>..HEAD --oneline -- ':!docs/'

# Empty = fresh (no non-docs commits since last generation)
# Non-empty = stale (regenerate)
```

The `':!docs/'` pathspec excludes the docs directory so that a docs-only commit (the one that wrote overview.md) doesn't itself appear as a change requiring regeneration.
