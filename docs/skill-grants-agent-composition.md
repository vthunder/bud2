---
topic: Skill Grants & Agent Composition
repo: bud2
generated_at: 2026-04-07T00:00:00Z
commit: 2f34b931
key_modules: [internal/executive/agent_defs.go, internal/executive/profiles.go, state/system/skill-grants.yaml]
score: 0.65
---

# Skill Grants & Agent Composition

> Repo: `bud2` | Generated: 2026-04-07 | Commit: 2f34b931

## Summary

The skill grants system controls which behavioral skills (SKILL.md files) each agent type receives when it is spawned. It replaces the earlier approach where each agent YAML file declared its own `skills:` list, centralizing that policy into `state/system/skill-grants.yaml`. Agent composition is the complementary mechanism that assembles a final system prompt and tool list from an agent's body, its granted skills, and the tool grants declared in `extensions.yaml`.

## Key Data Structures

### `SkillGrants` (`internal/executive/agent_defs.go`)
Loaded from `state/system/skill-grants.yaml`. Holds a single map from pattern strings to skill name lists. Patterns are matched against an agent's `"namespace:agent"` key at spawn time.
```go
type SkillGrants struct {
    Grants map[string][]string `yaml:"grants"` // pattern → skill names
}
```
An absent grants file is not an error — `LoadSkillGrants` returns an empty struct, causing all agents to fall back to their own `agent.Skills` field.

### `Agent` (`internal/executive/profiles.go`)
Represents a parsed agent definition file from `state/system/plugins/<namespace>/agents/<name>.yaml|.md`. The `Skills` field is the fallback used only when no grants file entry applies. `Body` is the markdown body appended after the YAML frontmatter; it becomes the lead section of the assembled system prompt.
```go
type Agent struct {
    Name        string
    Description string
    Level       string
    Model       string   // sonnet / opus / haiku
    Skills      []string // fallback: used only if no grants file entry matches
    Tools       []string // extra tools beyond the base set
    Body        string   // markdown body from the agent file
}
```

### `AgentAliases` (`internal/executive/profiles.go`)
Loaded from `state/system/agent-aliases.yaml`. Contains two maps: one for agent name remapping and one for skill name remapping. The skill alias map is consulted at composition time, allowing skills to be renamed without updating all agent YAML files.
```go
type AgentAliases struct {
    Agents map[string]string // alias → resolved file path
    Skills map[string]string // skill alias → resolved skill name
}
```

### `pluginDir` (`internal/executive/simple_session.go`)
Associates a local plugin directory path with tool grants from its manifest entry. Used in `LoadAllAgents` to apply per-plugin tool restrictions.
```go
type pluginDir struct {
    Path       string
    ToolGrants map[string][]string // agent pattern → tool names
}
```

### `claudecode.AgentDefinition` (SDK)
The final assembled representation passed to the Claude Agent SDK's `WithAgents` option. Fields: `Description`, `Prompt` (fully assembled system prompt), `Tools` (deduplicated list), `Model`.

## Lifecycle

1. **Startup / per-prompt reload**: `ExecutiveV2.Run` calls `LoadAllAgents(statePath, knownMCPTools)` on every prompt. This means any change to agent YAML files or `skill-grants.yaml` takes effect without a daemon restart.

2. **Plugin discovery**: `allPluginDirsForAgents(statePath)` collects two sets of plugin directories:
   - Local plugins: directories under `state/system/plugins/` (via `scanLocalPlugins`)
   - Manifest plugins: GitHub repos declared in `state/system/extensions.yaml`, cloned to `~/Library/Caches/bud/plugins/<owner>/<repo>/`. Each carries its `tool_grants` from the manifest.

3. **Agent file enumeration**: For each plugin dir, `LoadAllAgents` reads all `.yaml` and `.md` files in the `agents/` subdirectory. The agent key is `"<namespace>:<agentName>"` where namespace is the plugin directory name.

4. **Skill resolution** — grants file wins over agent YAML:
   - `resolveGrantedSkills(grants, key)` is called with the agent key.
   - Match priority: (1) exact key match, (2) `"namespace:*"` wildcard, (3) filepath.Match patterns like `"autopilot-*:planner"`, (4) global `"*"` wildcard.
   - If any entry matches, its skill list is used exclusively. The agent's own `Skills` field is ignored.
   - If no entry matches (grants file has no applicable rule), `agent.Skills` is used as the fallback.

5. **Skill content loading**: For each skill name, `LoadSkillContent(allPluginDirs, skillName)` searches all plugin dirs in order, looking for `skills/<name>/SKILL.md` then `skills/<name>.md`. Namespace prefixes (e.g. `"bud-ops:gk-conventions"`) are stripped to the short name before searching. The YAML frontmatter is stripped; only the body text is injected.

6. **Prompt assembly**: The final prompt is:
   ```
   ## Agent Behavioral Guide
   
   <agent body>
   
   ---
   
   <skill 1 content>
   
   ---
   
   <skill 2 content>
   ```
   If the agent has no body, the prompt is skill content only. Skills are separated by `\n\n---\n\n`.

7. **Tool list assembly**: The agent's `Tools` list is used as the starting set. `Agent(...)` declarations are normalized to plain `"Agent"`. Then, tool grants from the matching plugin manifest entry are appended. Wildcard tool patterns (e.g. `"mcp__bud2__gk_*"`) are expanded against `knownTools` (the live list of registered MCP tool names).

8. **AgentDefinition registration**: The assembled `claudecode.AgentDefinition` (prompt + tools + model) is stored in the `defs` map under the `"namespace:agent"` key and passed to the SDK.

### Subagent spawning path (non-SDK)

For subagents spawned via `Agent_spawn_async` (the MCP tool), `ResolveSubagentConfig(statePath, agentName, baseTools)` is used instead. This path does **not** consult `skill-grants.yaml` — it reads skill names directly from `agent.Skills`. This is a behavioral difference: the grants file only applies to agents used with the Claude Agent SDK's `WithAgents` option (i.e., the `LoadAllAgents` path).

## Design Decisions

- **Grants file wins over agent.Skills**: When an entry in `skill-grants.yaml` matches, it completely replaces `agent.Skills`. This is intentional — the grants file is the authoritative policy. An empty list (`[]`) in the grants file explicitly strips all skills from an agent, which cannot be expressed by omitting the file.

- **`"bud:*": []` pattern**: The `skill-grants.yaml` ships with `"bud:*": []`, which strips skills from all `bud`-namespace agents by default, then individual entries like `"bud:researcher"` add specific skills back. This means new `bud:` agents get zero skills unless explicitly granted — a secure default.

- **Global wildcard applies only when nothing else matches**: The `"*"` wildcard is evaluated last (priority 4). This ensures `gk-conventions` (granted globally) only reaches agents that no more-specific pattern covers. An exact match or namespace wildcard takes full precedence.

- **Hot-reload on every prompt**: `LoadAllAgents` is called each time the executive runs a session, not at startup. This trades a small per-prompt filesystem cost for the ability to iterate on agent definitions and skills without restarting the daemon.

- **Skill namespace prefix stripping**: `LoadSkillContent` strips the namespace prefix (e.g. `"bud-ops:gk-conventions"` → `"gk-conventions"`) before searching. This means the grants file can use namespaced skill names for clarity, but the lookup is always by short name across all plugin dirs.

- **Missing skills are non-fatal**: If a skill file is not found, `LoadAllAgents` silently skips it (logs only; no error returned). This prevents a missing plugin from breaking agent spawning.

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `internal/executive/agent_defs.go` | `internal/executive/profiles.go` | Calls `parseAgentData`, `LoadSkillContent`, `LoadAgentAliases` |
| `internal/executive/agent_defs.go` | `internal/executive/simple_session.go` | Calls `allPluginDirsForAgents`, `allPluginDirs`, `matchesAgentPattern`, `expandToolGrants` |
| `internal/executive/executive_v2.go` | `internal/executive/agent_defs.go` | Calls `LoadAllAgents` on every prompt, passing `knownMCPTools` |
| `internal/executive/executive_v2.go` | `internal/executive/profiles.go` | Calls `ResolveSubagentConfig` for subagents spawned via MCP tool |
| `state/system/skill-grants.yaml` | `internal/executive/agent_defs.go` | Loaded by `LoadSkillGrants`; drives skill policy for SDK-spawned agents |
| `state/system/extensions.yaml` | `internal/executive/simple_session.go` | `resolvedManifestPluginDirs` reads manifest and returns dirs with `ToolGrants` |
| Plugin `agents/*.yaml` | `internal/executive/agent_defs.go` | Agent body, tool list, model, and fallback skill list |
| Plugin `skills/**` | `internal/executive/profiles.go` | Skill body text injected into assembled system prompt |

## Non-Obvious Behaviors

- **`ResolveSubagentConfig` ignores `skill-grants.yaml`**: The MCP-tool subagent path reads `agent.Skills` directly and never loads the grants file. Only the SDK `WithAgents` path (via `LoadAllAgents`) applies grant-based skill policy. An agent can behave differently depending on whether it is spawned as an SDK agent or a MCP-tool subagent.

- **Empty grants list explicitly overrides**: `"bud:*": []` sets skills to an empty list and is treated as a match. The fallback to `agent.Skills` only fires when `resolveGrantedSkills` returns `ok == false` (no match). A match with an empty list is a valid, intentional override.

- **Namespace is the plugin directory name, not a declared field**: The `"namespace:agent"` key is derived from `filepath.Base(pd.Path)` — the directory name of the plugin — not from any field in the agent YAML. If a plugin directory is renamed, all its agent keys change.

- **Tool grants from extensions.yaml are additive**: Tool grants from the manifest entry are appended after the agent's own `Tools` list. They never remove tools. A plugin can only grant additional tools to its agents, not restrict them.

- **Skill search is first-match across all plugin dirs**: `LoadSkillContent` returns the first matching file found in the ordered list of plugin dirs. Local plugins (`state/system/plugins/`) are scanned before manifest-cloned plugins. If two plugins both define a skill with the same short name, the local one wins.

- **Model field maps to SDK enum at composition time**: The string `"sonnet"` / `"opus"` / `"haiku"` in the agent YAML is converted to the SDK's `AgentModel` enum inside `parseAgentModel`. An unrecognized value silently maps to `AgentModelInherit` (uses the parent session's model).

## Start Here

- `state/system/skill-grants.yaml` — the live grants configuration; edit here to change which skills any agent receives
- `internal/executive/agent_defs.go` — `resolveGrantedSkills` (line 42) and `LoadAllAgents` (line 91): the full assembly pipeline for SDK agents
- `internal/executive/profiles.go` — `ResolveSubagentConfig` (line 383) and `LoadSkillContent` (line 165): the assembly path for MCP-tool subagents
- `internal/executive/simple_session.go` — `allPluginDirsForAgents` (line 335) and `resolvedManifestPluginDirs` (line 272): how plugin directories are discovered
- `state/system/extensions.yaml` — manifest entries include `tool_grants` fields that grant MCP tools to specific agents in that plugin
