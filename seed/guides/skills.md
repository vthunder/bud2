# Skills Guide

Skills are prompt templates stored at `state/system/skills/<name>/SKILL.md` — the canonical location in the bud2 repo. `~/.claude/skills/<name>` are symlinks to these files; Claude Code loads skills from that symlink target. Each session loads their names and descriptions into `<system-reminder>`, making them available to Bud without reading the full file. When invoked via the `Skill` tool, the full SKILL.md body loads into the current session.

## When to Use Skills

Scan the available skill list at the start of each user interaction. Invoke a skill when:
- The user's request matches a skill's trigger description (e.g. "create a prd", "convert to ralph format")
- A task type is clearly within a skill's domain, even if not explicitly requested

Current skills and their triggers:
- **prd**: Planning features, writing requirements ("create a prd", "plan this feature", "requirements for X")
- **ralph**: Converting existing PRDs to prd.json for autonomous execution
- **made**: Structured decision evaluation for non-obvious choices with multiple viable approaches
- **web-research**: Deep web research on a topic
- **code-review**: Code review for a PR or changeset
- **vm-control**: Observe and control the Sandmill Mac OS 8 emulator (screenshots, clicks, typing). Use when debugging the emulator state or running interactive VM sessions.
- **handle-subagent-complete**: Process a completed subagent (retrieve output, close task, approve memories, act on next.action). Invoke when woken for a subagent-done focus item.

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

Skills live at `state/system/skills/<name>/SKILL.md`. After creating the SKILL.md, add a symlink:
```bash
ln -s /Users/thunder/src/bud2/state/system/skills/<name> ~/.claude/skills/<name>
```
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
