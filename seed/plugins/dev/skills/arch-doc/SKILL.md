---
name: arch-doc
description: "Generate a deep-dive architectural doc for a specific topic in a repository. Trigger: 'arch-doc', 'document this topic', 'deep-dive on', 'write arch doc for'."
user-invocable: true
---

# arch-doc

Generate a deep-dive architectural document for a specific topic (e.g. "Session Lifecycle & Context Assembly") in a repository. Topics span multiple modules and capture intent, data flow, design decisions, and non-obvious behaviors that can't be inferred from code structure alone.

## Input

- `topic` — the topic name (e.g. `"Session Lifecycle & Context Assembly"`)
- `repo_name` — short name (e.g. `bud2`). Default: infer from current directory or ask.
- `--autonomous` — non-interactive mode. Auto-commits without prompting. Intended for `dev:doc-maintain`.

## Steps

### 1. Find the repo and doc directory

Use the same discovery logic as `repo-doc`:
1. Check `~/src/<repo_name>/docs/overview.md` → `maintained`, `doc_dir = ~/src/<repo_name>/docs/`
2. Check `state/projects/<repo_name>/overview.md` → `reference`, `doc_dir = state/projects/<repo_name>/`
3. If neither exists: tell the user to run `dev:repo-doc` first and stop.

### 2. Look up topic in doc-plan.md

Read `<doc_dir>/doc-plan.md`. Find the row matching `topic` (case-insensitive, partial match ok).

Extract:
- `key_modules` — the module paths listed in the "Key Modules" column
- `score` — the topic score (for frontmatter)
- `status` — `missing` or `stale`

If no matching row found: tell the user and list available topics. Stop.

**Determine output filename:**
- Convert topic name to lowercase kebab-case slug (e.g. `"Session Lifecycle & Context Assembly"` → `session-lifecycle-context-assembly`)
- Output file: `<doc_dir>/<slug>.md`

If `status == "stale"`: note the existing file age. Regenerate anyway (the doc-plan already determined it's stale).

### 3. Capture HEAD SHA

```bash
SOURCE_SHA=$(git -C <repo_path> rev-parse HEAD)
```

### 4. Extract source for topic modules

Run repomix scoped to the topic's key modules:

```bash
repomix \
  --include "<module1>/**,<module2>/**,<module3>/**" \
  --compress \
  --style plain \
  --output /tmp/arch-doc-<slug>/compressed.md \
  <repo_path>
```

Also get the file tree for those modules:

```bash
repomix \
  --include "<module1>/**,<module2>/**,<module3>/**" \
  --no-files \
  --style plain \
  --output /tmp/arch-doc-<slug>/tree.md \
  <repo_path>
```

If repomix is unavailable, fall back to reading key files directly:
- List files under each module path with `find <module_path> -name "*.go" -o -name "*.ts" -o -name "*.py"` (or equivalent)
- Read the 10 largest or most central files directly using the Read tool
- Note in output: `extraction: fallback (repomix unavailable)`

Also read `<doc_dir>/overview.md` for cross-module context.

### 5. Synthesize the arch doc

Feed inputs into the synthesis prompt below. Generate the arch doc.

---

## Arch-Doc Synthesis Prompt

You are generating an architectural deep-dive document for a specific topic in a code repository. This document is used by engineers (including AI agents) to deeply understand a cross-cutting subsystem — beyond what can be inferred from reading the code structure.

**Inputs provided:**
- `topic` — the architectural topic (e.g. "Session Lifecycle & Context Assembly")
- `key_modules` — the primary modules this topic spans
- `compressed.md` — repomix compressed output scoped to those modules (structure + signatures)
- `overview.md` — the repo-level structural overview

**Instructions:**

1. **Summary** (2–3 sentences): What this subsystem does and why it exists. What problem does it solve?

2. **Key Data Structures**: List the 3–8 most important types, structs, interfaces, or schemas. For each: the type name (with file path), what it represents, and any invariants or important constraints. Be precise — use actual field names from the code, not abstractions.

3. **Lifecycle / Flow** (core of the doc): Trace the primary operation or lifecycle of this subsystem step by step. Number the steps. For each step: what happens, which function/method handles it, what state is mutated, and what is emitted or returned. If there is a state machine (explicit or implicit), describe the states and transitions. Mark inferences: write "likely" when you are inferring from signatures rather than reading implementation.

4. **Design Decisions**: What deliberate tradeoffs or architectural choices are visible in the code? Why does this design exist? Reference specific patterns (e.g., "the executor holds a mutex around X to prevent Y", "the queue is unbuffered because Z"). Only include what you can actually observe — do not invent decisions.

5. **Integration Points**: How does this subsystem connect to others? List each integration: `<this module>` → `<other module>` with a one-sentence description of what crosses the boundary (call, event, shared type, etc.).

6. **Non-Obvious Behaviors**: List 3–6 things that would surprise a new engineer reading this code. These are behaviors that are correct but not immediately obvious from the signatures alone.

7. **Start Here**: For someone starting a task in this area, list 3–5 specific files to read first with one-liner rationale for each.

**Tone and style:**
- Write for engineers who know the language but are new to this subsystem
- Be precise about what you observed vs. inferred — do NOT fabricate
- Prefer concrete (actual function names, type names) over abstract
- If a section has no useful content (e.g., no non-obvious behaviors found), omit it rather than padding

**Output format:**

```markdown
---
topic: <topic name>
repo: <repo_name>
generated_at: <ISO timestamp>
commit: <short SHA>
key_modules: [<list>]
score: <score from doc-plan>
---

# <Topic Name>

> Repo: `<repo_name>` | Generated: <date> | Commit: <short SHA>

## Summary

<2–3 sentences>

## Key Data Structures

### `TypeName` (`path/to/file.go`)
<what it is, key fields, invariants>

### `TypeName2` (`path/to/file.go`)
<...>

## Lifecycle

1. **Step name**: <what happens, which function, what state mutates>
2. ...

## Design Decisions

- **Decision**: <explanation with code evidence>
- ...

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `internal/foo` | `internal/bar` | <description> |

## Non-Obvious Behaviors

- **Behavior**: <explanation>
- ...

## Start Here

- `path/to/file.go` — why to read this first
- ...
```

---

### 6. Write the output

Write the synthesized doc to `<doc_dir>/<slug>.md`.

For **maintained repos** only:
- If `--autonomous`: run the commit directly without prompting.
- Otherwise, prompt:
  ```
  Arch doc written to <doc_dir>/<slug>.md

  Run to commit:
    git -C <repo_path> add docs/ && git -C <repo_path> commit -m 'docs: add arch-doc for <topic>'

  Commit now? [y/n]
  ```
  If confirmed, run the commit.

Commit command (used in both paths):
```bash
git -C <repo_path> add docs/ && git -C <repo_path> commit -m 'docs: add arch-doc for <topic>'
```

### 7. Summary

```
Generated arch doc for `<topic>` in `<repo_name>`:
- <slug>.md — <N> sections, covering <key_modules>

Next: run `dev:arch-doc "<next topic>"` or `dev:doc-audit` to scan stale docs.
```
