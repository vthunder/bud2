# ClaWHub Skill Compatibility — Task Plan

> Generated: 2026-04-12 | Area: Bud / Skills
> Add these to Things under the Bud area.

---

## Task 1 — Skill prerequisite checking (`requires.{bins,env}`)

**Area:** Bud  
**Complexity:** Medium

### What to build

1. After downloading/updating a ClaWHub skill, parse `metadata.clawdbot.requires` from `_meta.json` (or from the SKILL.md frontmatter). Check:
   - `requires.bins`: for each binary name, run `which <bin>` — warn if not found
   - `requires.env`: for each env var name, check `os.Getenv` — warn if empty

2. Surface warnings in three places:
   - **On download** (`downloadClawhubSkill` in `extensions.go`): log a warning listing missing prerequisites
   - **On first session load** (`cachedPlugins` or `loadManifestSkills`): attach a `<!-- skill prereq warning: ... -->` note that gets injected alongside the skill content so Claude sees it
   - **On invoke** (inside the session, when Claude invokes the skill via `Skill` tool): the injected warning tells Claude which prerequisites are missing and asks it to help the user set them up

3. Also handle `metadata.clawdbot.always: true` — skills with this flag should be injected into every session without explicit invocation. Add a pass in `LoadAllAgents` or `cachedPlugins` that collects "always-on" skill content.

4. **ClaWHub webpage instructions**: `_meta.json` contains a `source_url` field. When prerequisites are missing, fetch the skill's ClaWHub webpage and extract the installation/setup section. Surface this to the user (via the prerequisite warning note or via `talk_to_user` if in an interactive session). If env vars are missing and the page has clear instructions, Claude can offer to walk the user through setup.

**Files to touch:**
- `internal/executive/extensions.go` — `downloadClawhubSkill`, `loadManifestSkills`
- `internal/executive/profiles.go` — `LoadSkillContent` (attach prereq warnings)
- `internal/executive/simple_session.go` — `cachedPlugins` (collect always-on skills)

---

## Task 2 — `${CLAUDE_SKILL_DIR}` expansion and `!` shell injection in SKILL.md

**Area:** Bud  
**Complexity:** Small-Medium

### What to build

When bud loads a SKILL.md body (in `LoadSkillContent`), before returning the content:

1. **Expand `${CLAUDE_SKILL_DIR}`** — replace all occurrences of `${CLAUDE_SKILL_DIR}` with the absolute path to the skill's directory. This makes markdown links like `[See reference](${CLAUDE_SKILL_DIR}/references/api.md)` resolve to real paths that Claude's `Read` tool can open.

2. **Pre-expand `!` shell injection blocks** — the AgentSkills spec allows `` !`command` `` syntax in SKILL.md bodies. Claude Code executes these at load time and inlines the output. Implement the same in bud: scan the body for `` !`...` `` patterns, execute the command with the skill dir as cwd, and inline the stdout. This is how `references/` files get included when a skill author writes `` !`cat ${CLAUDE_SKILL_DIR}/references/guide.md` ``.

3. Security: sandbox the shell injection execution — run in the skill's own directory with no network access; cap output size; timeout at 5s.

**Provider-agnostic:** Expanding variables and shell injections at load time in Go means any downstream provider (claude-code, opencode-serve, openai-compatible) gets the resolved content without changes.

**Files to touch:**
- `internal/executive/profiles.go` — `LoadSkillContent`

---

## Task 3 — Skill lifecycle hooks (analogous to Claude Code hooks)

**Area:** Bud  
**Complexity:** Large — scope carefully, implement incrementally

### Background

Claude Code exposes ~26 hook events (`UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `SessionStart`, etc.) via `settings.json` shell script wiring. The `pskoett/self-improving-agent` skill uses `UserPromptSubmit` hooks (wired via `scripts/activator.sh`) to inject context at every turn.

Bud cannot and must not modify `~/.claude/settings.json` — bud running as daemon and a user running `claude` on the CLI must not interfere. The solution is to implement the hook mechanism natively in bud's execution layer.

### Design

A skill package can declare hooks by including a `hooks/bud/` directory (analogous to `hooks/openclaw/`). Each file in that directory is named after the event it handles (e.g. `hooks/bud/UserPromptSubmit`, `hooks/bud/PreToolUse`). At session setup, bud discovers and wires these scripts into the appropriate execution points.

Hook payload format should be **identical** to Claude Code's format (JSON on stdin, JSON on stdout, exit codes) so that skills designed for Claude Code hooks can target `hooks/bud/` with the same scripts.

**Provider abstraction:** The hook execution interface must be defined at the `Provider`/`Session` level (`internal/executive/provider/provider.go`) so it works across claude-code, opencode-serve, and openai-compatible backends. For the claude-code provider specifically, hooks should be wired via the SDK's hook API (if available) rather than via settings.json.

### Prioritized implementation order

**Phase 1 (high value):**
- `UserPromptSubmit` — inject context before every user prompt; what most "always-on" skills use
- `PreToolUse` / `PostToolUse` — safety rails, audit logging, input mutation
- `SessionStart` — bootstrap/env injection at session open
- `Stop` / `SubagentStop` — pipeline orchestration, chaining

**Phase 2 (medium value):**
- `PreCompact` / `PostCompact` — context preservation notes before/after compaction
- `StopFailure` — retry/fallback logic on provider errors

**Phase 3 (defer):**
- `TaskCreated` / `TaskCompleted` / `TeammateIdle` — team workflows (relevant if bud adds multi-agent task tracking)
- `ConfigChange`, `CwdChanged`, `FileChanged` — reactive env management
- `WorktreeCreate` / `WorktreeRemove` — worktree lifecycle
- `Elicitation` / `ElicitationResult` — MCP elicitation

### Sub-tasks

- [ ] Define `HookEvent` type and `HookHandler` interface in `internal/executive/provider/`
- [ ] Wire `UserPromptSubmit` in `ExecutiveV2.buildPrompt` (phase 1)
- [ ] Wire `PreToolUse`/`PostToolUse` at the session layer (phase 1)
- [ ] Wire `SessionStart`/`Stop` in session open/close (phase 1)
- [ ] Add hook discovery to `allPluginDirs` (scan `hooks/bud/` per skill dir)
- [ ] Document `hooks/bud/` convention in skills.md guide
- [ ] Phase 2 events (subsequent tasks)

**Files to touch:**
- `internal/executive/provider/provider.go` — `Session` interface: add hook wiring point
- `internal/executive/executive_v2.go` — `buildPrompt`, session open/close
- `internal/executive/profiles.go` or `simple_session.go` — hook discovery
- `internal/executive/provider/claude_code.go` — SDK hook wiring (if SDK supports it)
- `state-defaults/system/guides/skills.md` — document `hooks/bud/` convention

---

## Task 4 — `allowed-tools` SKILL.md field enforcement (design + defer)

**Area:** Bud  
**Complexity:** Small design, Medium implementation

### What to design (now) / build (later)

SKILL.md frontmatter can declare:
```yaml
allowed-tools:
  - Bash
  - Read
  - mcp__bud2__gk_*
```

Bud currently reads but ignores this field.

**Design decision to make:** The skill's `allowed-tools` should be **intersected** with the invoking agent's tool grants (from `extensions.yaml`), not replace them. I.e., an agent can never get more tools from a skill than it already has — `allowed-tools` can only further restrict.

**Implementation options:**
1. At `LoadAllAgents` time: intersect each agent's tool list with `allowed-tools` of its granted skills
2. At session time: pass the intersected list to the provider session

**Defer implementation** — the current agent-level grant system already controls tool access adequately. Create this task to track the design work and revisit when `hooks/` implementation is underway (they share the same skill metadata parsing path).

---

## Task 5 — Research and implement OpenClaw-compatible hooks (detailed spec)

**Area:** Bud  
**Complexity:** Research only for now (implementation covered by Task 3)

### What was researched

OpenClaw hooks mirror the Claude Code hooks spec exactly (both implement AgentSkills standard). Full 26-event spec is documented in `docs/clawhub-skill-compatibility-research.md` (Gap 5 section) and covers:

- **Session events**: `SessionStart`, `SessionEnd`, `InstructionsLoaded`
- **Turn events**: `UserPromptSubmit`, `Stop`, `StopFailure`  
- **Tool events**: `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, `PermissionDenied`
- **Subagent events**: `SubagentStart`, `SubagentStop`
- **Task events**: `TaskCreated`, `TaskCompleted`, `TeammateIdle`
- **Async events**: `Notification`, `ConfigChange`, `CwdChanged`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `PreCompact`, `PostCompact`
- **MCP events**: `Elicitation`, `ElicitationResult`

### Key design note for provider-agnostic support

The hook payload format (JSON stdin/stdout + exit codes) is the same across Claude Code and OpenClaw. Implementing it in bud's Go layer means:
- Skills with `hooks/openclaw/` can be supported by adding a `hooks/bud/` directory with the same scripts (or symlinking)
- Future OpenCode/GLM provider support: the `Provider`/`Session` interface must expose hook registration so each provider backend can wire hooks in its native way
- Design this interface in Task 3 with future providers in mind from the start

This task is complete as a research artifact. Implementation is tracked in Task 3.

---

## Notes on provider-agnostic design (applies to all tasks)

The user explicitly wants all features to work with non-Claude-Code providers (opencode-serve, openai-compatible/GLM-5.1). For each feature:

- **Prerequisites checking** (Task 1): Provider-neutral — runs in Go before any provider is involved
- **SKILL.md variable expansion** (Task 2): Provider-neutral — runs in Go at load time
- **Hooks** (Task 3): Must be defined in the `Provider`/`Session` interface so each backend can wire them natively. For opencode-serve and openai-compatible: implement hooks by wrapping the prompt/tool call pipeline in bud's exec layer (no reliance on provider-side hook support)
- **`allowed-tools`** (Task 4): Provider-neutral — tool list filtering happens in Go before session start
