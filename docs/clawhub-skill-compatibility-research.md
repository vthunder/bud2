---
topic: ClaWHub Skill Compatibility & Ecosystem Analysis
repo: bud2
generated_at: 2026-04-12T00:00:00Z
commit: 82261e9
status: research — gaps identified, tasks created
---

# ClaWHub Skill Compatibility & Ecosystem Analysis

> Generated: 2026-04-12 | Follows implementation commit `82261e9` (extensions.yaml + skills: support)

## Context

After implementing `extensions.yaml` skills loading (ClaWHub, git, local paths), two representative ClaWHub skills were analyzed for out-of-the-box compatibility with bud's skill injection pipeline. The analysis also covers the broader ClaWHub/AgentSkills/OpenClaw ecosystem to identify gaps worth closing.

---

## Skill Analyses

### `clawhub:trello` (steipete)

**What bud does today:** Downloads zip, extracts to `~/Library/Caches/bud/skills-clawhub/skills/trello/`, discovers SKILL.md via `LoadSkillContent`, injects body into agent system prompt. Works.

**What bud ignores:**
```yaml
metadata:
  clawdbot:
    requires:
      bins: [jq]
      env: [TRELLO_API_KEY, TRELLO_TOKEN]
```

The skill is injected regardless of whether `jq` is installed or `TRELLO_API_KEY`/`TRELLO_TOKEN` are set. Claude receives the skill instructions, attempts API calls, and fails at runtime with confusing errors.

**Additional structure:** SKILL.md only — no `references/`, no `scripts/`, no `hooks/`. Clean package.

**Verdict:** Core injection works. Gaps: no prerequisite checking.

---

### `clawhub:self-improving-agent` (pskoett)

**What bud does today:** Downloads zip, extracts full tree. SKILL.md body injected. Works.

**Package structure:**
```
SKILL.md
scripts/
  activator.sh        # intended as Claude Code pre-tool-use hook
  error-detector.sh   # intended as Claude Code post-tool-use hook
hooks/
  openclaw/
    agent:bootstrap   # OpenClaw-specific, fires on agent init
    tool:call         # OpenClaw-specific, fires before each tool call
    tool:result       # OpenClaw-specific, fires after each tool result
references/
  self-improvement-guide.md  # supplementary doc referenced from SKILL.md body
  error-patterns.md
assets/               # images, diagrams
```

**What bud ignores:**
- `scripts/`: Extracted to disk, never wired to Claude Code settings.json hooks
- `hooks/openclaw/`: OpenClaw-specific runtime hooks; not applicable to Claude Code or bud
- `references/`: Extracted to disk, not injected — SKILL.md body references them by name but they're unavailable to Claude

**Verdict:** Prompt injection works. Hooks and references pipeline is dead.

---

## Gap Inventory

### Gap 1: `requires.{bins,env}` — No prerequisite checking

**Current behavior:** Skill is injected even if listed prerequisites are missing.

**Desired behavior:**
- On skill download/install: warn if `bins` or `env` prerequisites are not satisfied
- On first load into a session: surface warning to user via `talk_to_user` or a system note
- On invoke: offer to help set up (e.g. walk through getting Trello API keys, setting env vars)
- Bonus: Read the skill's ClaWHub webpage (skills have a `source_url` in `_meta.json`) for installation/usage instructions; surface those to the user or act on them

**Scope note:** The ClaWHub webpage for a skill often has setup instructions not present in SKILL.md. `_meta.json` may contain the canonical URL; fetch and parse it.

---

### Gap 2: `references/` — Supplementary files not injected

**Current behavior:** `references/` directory extracted to disk, never served to Claude.

**Actual mechanism (researched):** The `references/` directory name has no special runtime magic in either Claude Code or OpenClaw. Files are extracted to disk; SKILL.md must explicitly reference them via:
1. **Markdown links** — SKILL.md body includes `[API Spec](references/api.md)` and Claude uses `Read` tool to load on-demand. `${CLAUDE_SKILL_DIR}` expands to the skill directory at load time.
2. **`!` shell injection** — SKILL.md includes `` !`cat ${CLAUDE_SKILL_DIR}/references/api.md` `` which inlines the file at load time (executed before injection).

**Desired behavior in bud:**
- At skill load time, pre-expand `!` shell injection blocks in SKILL.md (same as Claude Code does)
- Expand `${CLAUDE_SKILL_DIR}` variable in skill content to the actual skill directory path, so `Read` tool calls resolve correctly regardless of cwd
- For subagents using SDK injection: resolve these variables in the SKILL.md body before passing as prompt content

**Provider-agnostic constraint:** Expanding variables and shell injections at load time in Go (before content reaches any provider) makes this fully provider-neutral.

---

### Gap 3: `scripts/` — Claude Code hook auto-wiring

**Current behavior:** Shell scripts extracted to disk, never wired to Claude Code hooks.

**Desired behavior:**
- Do NOT modify `~/.claude/settings.json` or any `.claude/` directory
- Instead: at session setup time, wire scripts programmatically via the Claude Code SDK (e.g. `WithHooks(...)` if the SDK supports it), or implement the hook behavior natively in bud's exec layer
- Bud running as daemon + user running `claude` on CLI should be fully independent

**Provider-agnostic constraint:** Hook behavior must have an equivalent path for non-Claude-Code providers (opencode, openai-compatible). Design the abstraction now even if implementation is deferred.

---

### Gap 4: `allowed-tools` SKILL.md frontmatter field — Not read

**Current behavior:** `allowed-tools:` list in SKILL.md frontmatter is parsed but not enforced.

**Desired behavior:** Skills that declare a tool allowlist should have that enforced when they run — either by filtering the tool list passed to the session, or by injecting a constraint into the system prompt.

**Design question:** How does this interact with existing agent-level tool grants in `extensions.yaml`? The skill's `allowed-tools` should probably be intersected with the agent's grants, not replace them.

**Recommendation:** Defer implementation; create tracking task with design notes.

---

### Gap 5: `hooks/openclaw/` — OpenClaw runtime hooks

**Current behavior:** OpenClaw-specific hook files extracted to disk, completely ignored.

**Desired behavior:**
- Understand what hooks OpenClaw supports (see research below)
- Design analogous hook points in bud's executive/reflex layer
- Implement the highest-value ones; defer the rest

**OpenClaw hooks (researched):** OpenClaw mirrors the Claude Code hooks spec (both implement the AgentSkills standard). The hooks system uses `settings.json` and fires scripts/commands on lifecycle events. Full spec in `docs/openclaw-hooks-spec.md` (generated from research).

**Priority tiers for bud implementation:**
1. **High** — `UserPromptSubmit` (pre-prompt injection; what self-improving-agent uses), `PreToolUse`/`PostToolUse` (safety rails, input mutation), `Stop`/`SubagentStop` (pipeline orchestration), `SessionStart` (bootstrap/env injection)
2. **Medium** — `PreCompact`/`PostCompact` (context preservation), `StopFailure` (retry/fallback), `Notification` (alerting)
3. **Low/defer** — `TaskCreated`/`TaskCompleted`/`TeammateIdle` (team workflows), `ConfigChange`, `CwdChanged`, `FileChanged`, `WorktreeCreate`/`WorktreeRemove`, `Elicitation`/`ElicitationResult`

**Design constraint for bud:** Cannot modify `~/.claude/settings.json`. Hook wiring must go through the Go SDK or bud's own exec layer. The hook payloads (stdin JSON + stdout JSON + exit codes) can be replicated exactly — skills that target Claude Code hooks can be run by bud's hook engine without modification.

---

## Ecosystem Map

### AgentSkills (agentskills.io)
Open spec that defines the SKILL.md format. Multi-vendor: ClaWHub, Claude Code, OpenClaw, and Cursor all consume the same frontmatter. Key fields: `name`, `description`, `user-invocable`, `allowed-tools`, `metadata.clawdbot`. Bud reads `name` and `description`; ignores the rest.

### ClaWHub (clawhub.ai)
Skill registry built on Convex. Globally-unique slugs. HTTP zip download API:
```
https://wry-manatee-359.convex.site/api/v1/download?slug={slug}&version={ver}
```
No git backing. `_meta.json` in zip contains: `slug`, `version`, `source_url`, `description`, `metadata`. Bud implementation: `downloadClawhubSkill` in `internal/executive/extensions.go`.

### OpenClaw (openclaw.dev)
Self-hosted multi-channel agent gateway (Discord, Slack, terminal). Has its own hook system (`hooks/openclaw/` dir). Completely separate runtime from Claude Code and bud. Skills can target OpenClaw hooks in addition to / instead of Claude Code hooks. Bud's exec layer is analogous to OpenClaw's runtime — the hook mapping is conceptually sound.

### Clawdbot
ClaWHub's reference implementation bot. `metadata.clawdbot` frontmatter extension declares:
```yaml
metadata:
  clawdbot:
    requires:
      bins: [jq, curl]
      env: [API_KEY, SECRET]
    emoji: 🔗
    nix.plugin: jq
    install:
      - step description
    always: true       # always inject, regardless of invocation
    skillKey: trello   # used for deduplication
```
Bud should treat `metadata.clawdbot.requires` as the authoritative prerequisites field.

### vercel-labs `skills` npm package
Node.js pattern for bundling tool definitions as "skills" for AI SDK agents. Different format entirely — not SKILL.md based. Low relevance to bud.

---

## Open Questions

1. **`references/` injection mechanism**: What does the AgentSkills spec say? What does Claude Code's `Skill` tool do with `references/`? (Research task pending)
2. **OpenClaw hook list**: Full catalog with fire conditions and payloads (Research task pending)
3. **Provider-agnostic hook abstraction**: What's the right interface for pre/post tool-call hooks that works across claude-code, opencode-serve, and openai-compatible providers?
4. **`metadata.clawdbot.always`**: Skills with `always: true` should be injected into every session — bud doesn't implement this. Worth implementing alongside `requires` checking.

---

## Related Files

- `internal/executive/extensions.go` — ClaWHub download, git skill cloning, zip extraction
- `internal/executive/simple_session.go` — `allPluginDirs`, `cachedPlugins`, manifest loading
- `internal/executive/profiles.go` — `LoadSkillContent` (SKILL.md injection)
- `internal/executive/executive_v2.go` — Session setup, where SDK hook wiring would go
- `state/system/extensions.yaml` — Live manifest for skills: entries
- `state-defaults/system/guides/skills.md` — Skills guide surfaced to Claude
