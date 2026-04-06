---
name: doc-maintain
description: "Autonomous doc maintenance: pick the repo that would benefit most from documentation work right now, and make one meaningful improvement. Trigger: 'doc-maintain', 'maintain docs', idle fallback."
user-invocable: true
---

# doc-maintain

Autonomous documentation maintenance. Pick the repo that would benefit most from documentation work right now and make **one meaningful improvement** — then stop. One bounded unit of work per invocation.

Intended as an idle-fallback task: run this when no other queued work is pending.

## What counts as "one meaningful improvement"

In priority order:
1. **Stale overview** — regenerate overview + doc-plan for a repo with >20 non-docs commits since last generation
2. **Needs audit** — run doc-audit for a maintained repo that has never been audited (no `docs/archive/` dir and >5 non-generated docs)
3. **Missing arch doc** — write the top-ranked missing arch-doc topic for a repo that already has a clean doc-plan

**Repos with no existing overview are skipped.** Overviews must be bootstrapped interactively (run `dev:repo-doc` manually). Once an overview exists — in either `~/src/<repo>/docs/` or `state/projects/<repo>/` — autonomous runs will maintain it.

Only one of these is done per invocation. Stop after completing it.

## Steps

### 1. Run doc-scan

Invoke `dev:doc-scan` to get the cross-repo state snapshot. This is fast (walks `~/src/*/`, reads git metadata only).

Collect:
- **Undocumented repos** — no `docs/overview.md` or `state/projects/*/overview.md`
- **Stale repos** — docs exist but N non-docs commits have landed since last generation
- **Current repos** — docs and code in sync

### 2. Score each candidate

**Skip any repo with no overview** — if neither `~/src/<repo>/docs/overview.md` nor `state/projects/<repo>/overview.md` exists, exclude it entirely. Do not generate overviews autonomously.

For remaining repos (those with an existing overview), compute an improvement priority score:

```
priority = staleness_commits(normalized, 0–50) + has_no_audit(30) + top_topic_missing(20)
```

- `staleness_commits`: proportional to commit count since last generation, normalized across candidates (max 50 points)
- `has_no_audit`: 30 points if `docs/archive/` does not exist (audit never run)
- `top_topic_missing`: 20 points if doc-plan exists and rank-1 topic has `missing` status

Pick the **highest-scoring repo**.

If no repo has an overview yet, or all repos with overviews are current: report "Nothing to do." and stop.

### 3. Determine the improvement action

For the selected repo, determine which improvement to make (in priority order):

| Condition | Action |
|-----------|--------|
| Stale overview (>20 commits) | Run `dev:repo-doc --autonomous` |
| Overview current, no audit run yet | Run `dev:doc-audit --autonomous` |
| Overview current, audit done, top topic missing | Run `dev:arch-doc "<rank-1 topic>" --autonomous` |
| Nothing applies | Report and stop |

Only one action per invocation.

### 4. Execute the improvement

Run the chosen skill with `--autonomous` flag. The sub-skill handles extraction, synthesis, writing, and committing without prompting.

If the sub-skill fails (repomix unavailable, extraction error, git conflict): log the failure, note the repo as temporarily blocked, and stop. Do not fall back to a different repo in the same invocation — that would make the unit of work unbounded.

### 5. Report

After completing (or failing), emit a brief summary:

```
doc-maintain: <repo_name>
  Action: <what was done>
  Improvement: <one-liner describing the specific output>
  Commit: <short SHA if committed, or 'uncommitted'>
```

If nothing was done (all repos current):
```
doc-maintain: nothing to do
  All repos have current docs. Next run will re-check.
```

## Invocation as idle fallback

The executive should invoke this skill (via a subagent) when:
- No Tasks are pending in the Bud Things area
- No queued focus items require attention
- No subagents are running

This is a background subagent invocation. The executive should not wait for it — spawn it and move on. When the subagent completes, `handle-subagent-complete` will process the result normally.

**Spawn pattern:**
```
Agent_spawn_async(
  profile: "coder",
  goal: "Read state/system/plugins/dev/skills/doc-maintain/SKILL.md and follow its instructions exactly. Sub-skills (doc-scan, repo-doc, doc-audit, arch-doc) are at state/system/plugins/dev/skills/<name>/SKILL.md — read them when doc-maintain references them. Use --autonomous flag throughout. Stop after one improvement.",
  context: "Idle fallback — no other tasks pending."
)
```

Note: the coder agent has `skills: []` and cannot use the `Skill` tool. All sub-skill logic must be loaded by reading the SKILL.md files directly with the `Read` tool.
