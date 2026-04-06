---
name: doc-audit
description: "Audit existing docs in a repository: classify each file relative to the doc-plan (fold-candidate, archive, keep), move archive-bound docs to docs/archive/. Trigger: 'doc-audit', 'audit docs', 'clean up docs', 'archive stale docs'."
user-invocable: true
---

# doc-audit

Audit the existing documentation in a repository. Classify every file relative to the generated doc-plan, move archive-bound docs to `docs/archive/`, and annotate fold-candidates for future `arch-doc` runs.

To scan multiple repos for undocumented or stale docs, use `dev:doc-scan` instead.

## Input

- `repo_name` — short name (e.g. `bud2`). Default: infer from current directory or ask.
- `--execute` — optional flag. If provided, actually move archive-bound files; otherwise only produce the action plan.
- `--autonomous` — non-interactive mode. Implies `--execute` and auto-commits without prompting. Intended for `dev:doc-maintain`.

## Steps

### 1. Find the repo and doc directory

Use the same discovery logic as `repo-doc` and `arch-doc`:
1. Check `~/src/<repo_name>/docs/overview.md` → `maintained`, `doc_dir = ~/src/<repo_name>/docs/`
2. Check `state/projects/<repo_name>/overview.md` → `reference`, `doc_dir = state/projects/<repo_name>/`
3. If neither exists: tell the user to run `dev:repo-doc` first and stop.

### 2. Inventory existing docs

List all `.md` files under `<doc_dir>` recursively. Exclude:
- `overview.md`
- `doc-plan.md`
- `README.md`
- `doc-meta.json`
- `docs/archive/**` (already archived)

Also scan the **repo root** for `.md` files. Exclude `README.md` (universal standard). Include all other `.md` files found directly in `<repo_path>/` (not subdirectories — those have their own conventions). Common root-level docs to check: `DESIGN.md`, `CHANGELOG.md`, `AGENTS.md`, `CHANGES-ANALYSIS.md`, repomix output files, etc.

For root-level archive files, move them to `docs/archive/root/` (preserving the distinction that they lived at the root).

For each file, note:
- Relative path from `<doc_dir>`
- Whether it has a `generated_at` / `commit:` frontmatter (marks it as a generated arch doc)
- Approximate age (from frontmatter `generated_at`, or git log for the file: `git -C <repo_path> log -1 --format="%ci" -- <file>`)

### 3. Read the doc-plan

**Guard — check before proceeding:**

1. **Missing doc-plan:** If `<doc_dir>/doc-plan.md` does not exist, stop and tell the user:
   > `doc-plan.md` not found. Run `dev:repo-doc` first to generate it, then re-run `dev:doc-audit`.

2. **Stale doc-plan:** Check the git timestamp of `doc-plan.md` vs HEAD:
   ```bash
   git -C <repo_path> log --format="%H" -- docs/doc-plan.md | head -1  # last commit that touched doc-plan
   git -C <repo_path> log <plan_commit>..HEAD --oneline -- . ':(exclude)docs/' | wc -l  # non-docs commits since
   ```
   If N > 10 non-docs commits have landed since the doc-plan was last generated, warn:
   > `doc-plan.md` may be stale (N commits since last generation). Classification results may be inaccurate.
   > Regenerate with `dev:repo-doc`? Proceeding with current doc-plan.
   
   Do not block — warn and continue. The user can choose to regenerate first.

If both checks pass, read `<doc_dir>/doc-plan.md` and extract the topic list (Rank, Topic, Key Modules, Status columns).

### 4. Classify each doc

Feed the inventory and doc-plan into the classification prompt below. Produce a classification table.

---

## Classification Prompt

You are auditing the existing documentation files in a code repository against its architectural doc-plan.

**Inputs:**
- `inventory` — list of docs with path, frontmatter presence, and age
- `doc-plan.md` — the ranked topic list of architectural docs this repo should have
- `overview.md` — repo overview for context

**For each doc in the inventory, assign one of these classifications:**

| Class | Meaning |
|-------|---------|
| `generated` | Has `generated_at` frontmatter — a current arch doc. Keep as-is. |
| `fold-candidate` | Contains useful content that should be incorporated as source context when a doc-plan topic's arch doc is written. Identify which topic(s) it feeds. |
| `keep` | Not an arch doc, but still operationally useful (playbooks, integration guides, operational notes). Not a fold-candidate. |
| `archive` | Historical, superseded, or already-executed plan. Should move to `docs/archive/`. |
| `unclear` | Cannot classify without more context; flag for human review. |

**For each `archive` file, also assign an archive status** (appended to the filename on move):

| Status | Meaning | Example triggers |
|--------|---------|-----------------|
| `executed` | An implementation plan or design spec that was built and shipped | `plans/` files, design docs with matching code in repo |
| `obsolete` | Describes a feature, component, or system that no longer exists | `v1/` docs, docs about deleted systems |
| `superseded` | A design or approach that was replaced by a different version | Design docs where a v2 took over, docs made redundant by a refactor |

Default to `obsolete` when unsure. Do not add a status to non-archive files.

**Rules:**
- If a file is in a `plans/` subdirectory and appears date-stamped or describes a past implementation: `archive` + `executed`
- If a file is in a `v1/` or `vN/` subdirectory: `archive` + `obsolete`
- If a file has `generated_at` frontmatter (arch doc): `generated`
- If a file describes a specific integration, tool, or process that is still active: lean toward `keep` or `fold-candidate`
- If content overlaps significantly with a doc-plan topic (same subject area): `fold-candidate`
- When `fold-candidate`, list which doc-plan topic(s) the content would feed

**Output format (write exactly this):**

```markdown
## Doc Audit: <repo_name> — <YYYY-MM-DD>

| File | Class | Archive Status | Feeds Topic / Notes |
|------|-------|---------------|---------------------|
| `path/to/file.md` | archive | executed | Dated implementation plan, already executed |
| `path/to/file.md` | archive | obsolete | v1 component, system no longer exists |
| `path/to/file.md` | archive | superseded | Replaced by v2 design |
| `path/to/file.md` | fold-candidate | — | "MCP Tool Dispatch" — describes tool registration |
| `path/to/file.md` | keep | — | Operational playbook, not arch doc material |
| `path/to/file.md` | generated | — | Current arch doc — no action needed |
| `path/to/file.md` | unclear | — | Unclear purpose — needs human review |

### Actions

**Archive** (move to `docs/archive/`, filename annotated with status):
- `path/to/file.md` → `docs/archive/path/to/file (executed).md` — reason

**Fold-candidates** (use as source context for arch-doc generation):
- `path/to/file.md` → feeds topic: "MCP Tool Dispatch"
  - Key content: <1 sentence on what useful detail this provides>

**Keep** (no action needed):
- `path/to/file.md` — reason

**Unclear** (needs human review):
- `path/to/file.md` — what is unclear
```

---

### 5. Present or execute

**Without `--execute`** (default — dry run):
- Print the classification table and actions.
- For `archive` files: show the `git mv` commands with annotated destination filenames.
- Tell the user: "Run `dev:doc-audit --execute` to apply these moves."

**With `--execute`** (or `--autonomous`):
- Create `<doc_dir>/archive/` if it doesn't exist.
- For each `archive` file, move it and annotate the filename with the archive status:
  - Strip the `.md` extension, append ` (<status>)`, re-add `.md`
  - Example: `non-interactive.md` with status `superseded` → `non-interactive (superseded).md`
  - Preserve subdirectory structure under `archive/`:

  **For maintained repos** (`doc_dir = ~/src/<repo_name>/docs/`):
  ```bash
  git -C <repo_path> mv docs/plans/2025-01-07-gtd-implementation.md \
      docs/archive/plans/2025-01-07-gtd-implementation (executed).md
  git -C <repo_path> mv docs/non-interactive.md \
      docs/archive/non-interactive (superseded).md
  ```

  **For reference repos** (`doc_dir = state/projects/<repo_name>/`):
  Docs are tracked in the state git repo (`~/src/bud2/state`), not the source repo. Use:
  ```bash
  STATE_ROOT=~/src/bud2/state
  git -C $STATE_ROOT mv state/projects/<repo_name>/plans/2025-01-07-foo.md \
      "state/projects/<repo_name>/archive/plans/2025-01-07-foo (executed).md"
  ```
  Note: quote paths with spaces in bash if running manually.

- Write `<doc_dir>/archive/README.md` (create or append):
  ```markdown
  # Archive

  Historical documents, superseded designs, and executed plans.
  These are preserved for reference but are not maintained.
  Filenames are annotated with their archive reason: `(executed)`, `(obsolete)`, or `(superseded)`.

  | File | Archived | Reason |
  |------|----------|--------|
  | `<file (status).md>` | <date> | <reason> |
  ```
- For **maintained repos**:
  - If `--autonomous`: run the commit directly without prompting.
  - Otherwise, prompt:
    ```
    Archive moves applied. Run to commit:
      git -C <repo_path> add docs/ && git -C <repo_path> commit -m 'docs: archive historical docs'
    Commit now? [y/n]
    ```

  Commit command (used in both paths):
  ```bash
  git -C <repo_path> add docs/ && git -C <repo_path> commit -m 'docs: archive historical docs'
  ```

- For **reference repos**: never commit to the source repo. If `--autonomous`, silently skip commit. Otherwise, note that files were moved in `state/projects/` and the state repo commit will be handled separately.

### 6. Annotate fold-candidates in doc-plan

For each fold-candidate, update `<doc_dir>/doc-plan.md`:
- Find the matching topic row
- Append a `Source:` note to the Signals cell: `Source: <relative-path>`

This makes `dev:arch-doc` aware of existing source material when it generates that topic's doc.

If `doc-plan.md` is being updated, add it to any pending git commit.

### 7. Summary

```
Doc audit for `<repo_name>`:
- <N> files classified
- <N> archived (moved to docs/archive/)
- <N> fold-candidates annotated in doc-plan
- <N> kept as-is
- <N> unclear (review manually)

Next: run `dev:arch-doc "<top-unwritten topic>"` to write the top unwritten doc, or `dev:doc-scan` to check all repos for stale/missing docs.
```
