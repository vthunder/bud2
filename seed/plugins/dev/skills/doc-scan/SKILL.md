---
name: doc-scan
description: "Scan all repos under ~/src/ to find which ones are undocumented or have stale docs. Trigger: 'doc-scan', 'scan repos', 'which repos need docs', 'find stale docs', 'audit all repos'."
user-invocable: true
---

# doc-scan

Scan all repositories under `~/src/` and report which ones need documentation attention. Outputs three groups: undocumented, stale, and current.

This is the cross-repo discovery companion to `dev:doc-audit` (which operates on a single repo) and `dev:repo-doc` (which generates docs for a single repo).

## Input

- `--src` — optional path override. Default: `~/src/`.
- `--stale-threshold` — number of non-docs commits before a repo is considered stale. Default: `10`.

## Steps

### 1. Discover repos

```bash
ls <src_dir>
```

For each directory under `<src_dir>`:
1. Confirm it is a git repo:
   ```bash
   git -C <src_dir>/<dir> rev-parse HEAD 2>/dev/null
   ```
   Skip non-git directories silently.

### 2. Check documentation status

For each git repo, check in order:
1. **Maintained docs:** `<src_dir>/<dir>/docs/overview.md`
2. **Reference docs:** `state/projects/<dir>/overview.md`

If neither exists → **undocumented**.

### 3. Check staleness for documented repos

For repos that have docs, find the commit that last touched `overview.md` and count non-docs commits since:

```bash
# Last commit to overview.md
PLAN_COMMIT=$(git -C <repo_path> log --format="%H" -- docs/overview.md | head -1)

# Non-docs commits since that point
git -C <repo_path> log ${PLAN_COMMIT}..HEAD --oneline \
  -- . ':(exclude)docs/' ':(exclude)*.md' | wc -l
```

If the count exceeds `--stale-threshold` (default 10) → **stale**. Otherwise → **current**.

### 4. Report

Output the results in three groups:

```
## Doc Scan — <YYYY-MM-DD>

### Undocumented (N)
No docs found. Run `dev:repo-doc` to generate.

| Repo | Last Commit |
|------|-------------|
| foo  | 2026-03-15  |

### Stale (N)
Docs exist but are behind code commits.

| Repo | Docs Last Updated | Commits Behind | Action |
|------|-------------------|----------------|--------|
| bud2 | 2026-02-10        | 34             | `dev:repo-doc bud2` |

### Current (N)
| Repo | Docs Last Updated |
|------|-------------------|
| bar  | 2026-04-01        |
```

### 5. Suggest next actions

After the table, emit a prioritized action list:

```
Suggested actions:
1. dev:repo-doc <most-stale-repo>   — N commits behind
2. dev:repo-doc <undocumented-repo> — no docs at all
...
```

Order by: undocumented first (highest priority), then stale sorted by commit count descending.
