# Skills Guide

Skills are prompt templates stored as plugin skills: `state/system/plugins/<plugin>/skills/<name>/SKILL.md`. Bud passes `--plugin-dir` for each plugin via SDK options at session start.

Claude Code loads skills from all plugins and surfaces their names and descriptions in `<system-reminder>` each session. When invoked via the `Skill` tool, the full SKILL.md body loads into the current session.

## When to Use Skills

Scan the available skill list at the start of each user interaction. Invoke a skill when:
- The user's request matches a skill's trigger description (e.g. "create a prd", "convert to ralph format")
- A task type is clearly within a skill's domain, even if not explicitly requested

Current skills and their triggers (grouped by plugin):

**bud-ops** — Bud operational skills:
- **handle-subagent-complete**: Process a completed subagent (retrieve output, close task, approve memories, act on next.action). Invoke when woken for a subagent-done focus item.
- **start-workflow**: Start a multi-step planning workflow ("plan X", "create a plan for X", or named workflow).
- **planning**: Execute MADE planning methodology — diverse candidates, binary rubrics, score, select. Use when conducting structured analysis at any planning level.
- **things-operations**: Interact with Things 3 task tracker — create, query, claim, update issues. All planning work goes to the Bud area.
- **gk-conventions**: Graph knowledge store conventions for planning agents — read guides, retrieve prior cycle data, store directions and observations.

**dev** — Development skills:
- **prd**: Planning features, writing requirements ("create a prd", "plan this feature", "requirements for X")
- **ralph**: Converting existing PRDs to prd.json for autonomous execution
- **web-research**: Deep web research on a topic
- **code-review**: Code review for a PR or changeset
- **repo-doc**: Generate or refresh overview.md and doc-plan.md for a code repository ("repo-doc", "generate overview", "document this repo")
- **arch-doc**: Generate a deep-dive architectural doc for a specific topic ("arch-doc", "document this topic", "deep-dive on")
- **doc-audit**: Audit existing docs in a repo — classify, archive, annotate fold-candidates ("doc-audit", "audit docs", "clean up docs")
- **doc-scan**: Scan all repos under ~/src/ for undocumented or stale docs ("doc-scan", "scan repos", "which repos need docs")
- **doc-maintain**: Autonomous doc maintenance idle fallback — picks one repo and makes one improvement ("doc-maintain", "maintain docs")

**sandmill** — Sandmill content skills:
- **blog**: Manage the sandmill.org blog pipeline — list ideas, research, draft, polish, publish ("blog status", "research post", "draft post", "publish post").
- **voice**: Interview the author to extract voice, then rewrite a blog post to match it ("voice interview", "rewrite this post", "doesn't sound like me").
- **vm-control**: Observe and control the Sandmill Mac OS 8 emulator (screenshots, clicks, typing). Use when debugging the emulator state or running interactive VM sessions.

**zettel** — Zettelkasten knowledge system:
- **zettel-search**: Search existing zettels before creating new ones ("search zettels", "find zettel", "does a zettel exist for"). Always run before zettel-new.
- **zettel-new**: Create a single new atomic zettel ("new zettel", "create zettel", "add to zettelkasten", "atomize this idea").
- **zettel-convert**: Convert a notes/ file into one or more zettels ("convert note to zettel", "atomize this note", "zettelify").
- **zettel-link**: Add a bidirectional link between two existing zettels ("link these zettels", "connect zettel", "relate two zettels").
- **zettel-index**: Build or rebuild a Map of Content for a tag ("build zettel index", "create MOC", "map of content for", "index zettels by tag").
- **zettel-archive**: Move an ephemeral notes/ file to notes/archive/ ("archive this note", "this note has no zettel value", "archive sprint brief").
- **zettel-lint**: Periodic health check of the zettel corpus ("lint zettels", "check zettel health", "find orphaned zettels", "zettel maintenance"). Run every 2–4 weeks or after bulk conversions.

Do NOT invoke a skill just because the topic is tangentially related. The skill's `description` field is authoritative — if the user's request doesn't match the trigger phrases, don't invoke it.

## How to Invoke

Call the `Skill` tool with the skill name. The full prompt template loads and guides the rest of the session.

```
Skill("prd")
Skill("ralph")
```

## Multi-Session Skill Work

Some skills produce output that spans multiple sessions (e.g., implementing a PRD). Track this in Things:
1. Create a Things task referencing the skill and its output file (e.g., `prd-feature-name.md`)
2. Use the task notes field to record the skill used and current step
3. On subsequent wakes, read the task to restore context

## Interactive Skills

Skills that ask clarifying questions work naturally with Bud's one-shot model:
- Skill asks question → Bud calls `talk_to_user` → session ends
- User responds → new P1 session picks up the conversation buffer
- Bud re-invokes the skill → continues from where it left off

The conversation buffer provides continuity. No architectural change needed.

## Adding New Skills

All skills live in plugins. Skill loading is automatic — no symlinks needed.

**Add to an existing plugin:**
1. Create `state/system/plugins/<plugin>/skills/<name>/SKILL.md`
2. Add to the "Current skills" list in this guide

**Create a new plugin:**
1. Create `state/system/plugins/<plugin>/.claude-plugin/plugin.json`
2. Add skills in `state/system/plugins/<plugin>/skills/<name>/SKILL.md`
3. Bud passes `--plugin-dir` for each plugin dir at session start via the SDK

Then add the new skill to the "Current skills" list in this guide.

Required frontmatter:
```yaml
---
name: skill-name
description: "One-line description with trigger phrases. Used by Bud to decide relevance."
user-invocable: true
---
```

After adding a skill, update the "Current skills" list in this guide so Bud can reference it without loading all skill files.

## Skill Grants

Skill assignments for agents are controlled centrally in `state/system/skill-grants.yaml` rather than in individual agent YAML files. The `skills:` field has been removed from agent files — this is intentional.

**File location:** `state/system/skill-grants.yaml`

**Pattern matching** (highest priority wins):
1. Exact match: `"autopilot-epic:planner"` — grants specific skills to one agent
2. Namespace wildcard: `"bud:*"` — grants to all agents in a namespace
3. Glob wildcard: `"autopilot-*:planner"` — matches across namespaces via `filepath.Match`
4. Global wildcard: `"*"` — fallback for all agents

**Adding a new skill grant:**

```yaml
grants:
  "mynamespace:myagent":
    - skill-name
    - another-skill
```

If the grants file is missing entirely, `LoadAllAgents` falls back to the `skills:` field in each agent YAML (backward compatibility). The absence of `skills:` in agent files is correct — do not add it back.

**Alias resolution:** Skill names in the grants file are resolved through `state/system/agent-aliases.yaml` before loading. For example, `issue-operations` resolves to `things-operations`. Use the canonical skill name (e.g. `things-operations`) in grants whenever possible.
