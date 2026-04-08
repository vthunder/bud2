---
topic: Plugin Manifest Runtime & Tool Grants
repo: bud2
generated_at: 2026-04-08T09:00:00Z
commit: c459af0
key_modules: [cmd/bud, internal/executive/simple_session.go, state/system/plugins.yaml]
score: 0.54
---

# Plugin Manifest Runtime & Tool Grants

> Repo: `bud2` | Generated: 2026-04-08 | Commit: c459af0

## Summary

The plugin manifest system allows external GitHub repos and local filesystem paths to register agent definitions, skills, zettel libraries, and per-agent tool grants into bud2 at runtime without modifying core configuration. At startup, `loadManifestPlugins` clones or updates each listed remote repo to `~/.cache/bud/plugins/` and resolves local paths directly; at agent-spawn time, `allPluginDirsForAgents` re-reads the already-cloned directories with their tool grants, applying any `exclude:` filters before handing plugin dirs to the agent-definition loader. Additionally, `generateZettelLibraries` scans all plugin dirs at each prompt invocation to regenerate `state/system/zettel-libraries.yaml`, wiring zettel paths declared in `plugin.json` files into the running system.

## Key Data Structures

### `pluginManifest` / `pluginManifestEntry` (`internal/executive/simple_session.go`)

`pluginManifest` is the in-memory representation of `state/system/plugins.yaml`. Each `pluginManifestEntry` captures one plugin source:

```go
type pluginManifestEntry struct {
    owner, repo  string              // remote GitHub repo (e.g. "vthunder", "useful-plugins")
    dir          string              // subdirectory within repo (empty = root)
    ref          string              // branch/tag/commit (empty = default branch)
    localPath    string              // alternative: local filesystem path
    ToolGrants   map[string][]string // agent pattern → list of tool names (may include wildcards)
    Exclude      []string            // sub-plugin directory names to skip (e.g. "issues-linear")
}
```

`ToolGrants` is the security boundary: it declares which MCP tools agents from this plugin are allowed to invoke. `Exclude` lets a manifest entry opt out of specific sub-plugins without forking the manifest or the upstream repo.

### `pluginDir` (`internal/executive/simple_session.go`)

Associates a resolved local filesystem path with the tool grants from its manifest entry:

```go
type pluginDir struct {
    Path       string
    ToolGrants map[string][]string // nil for local (non-manifest) plugins
}
```

Local plugins under `state/system/plugins/` always have `nil` ToolGrants — they can only use tools they declare in their agent YAML `tools:` field.

### `zettelLibrary` / `zettelLibrariesFile` (`internal/executive/simple_session.go`)

Generated at each `SendPrompt` call; written to `state/system/zettel-libraries.yaml`:

```go
type zettelLibrary struct {
    Name     string `yaml:"name"`
    Path     string `yaml:"path"`
    Default  bool   `yaml:"default,omitempty"`
    Readonly bool   `yaml:"readonly,omitempty"`
}
```

The `home` library (pointing to `state/zettels`) is always included as the default writable library. Plugins contribute additional libraries if their `plugin.json` declares a `"zettels"` path.

### `SkillGrants` (`internal/executive/agent_defs.go`)

Loaded from `state/system/skill-grants.yaml`. Maps agent identity patterns to skill names:

```go
type SkillGrants struct {
    Grants map[string][]string `yaml:"grants"` // pattern → skill names
}
```

Complements per-agent `skills:` YAML fields with a centralized override table.

### `Agent` (`internal/executive/profiles.go`)

Parsed in-memory representation of an agent definition file (`agents/*.md` or `agents/*.yaml`):

```go
type Agent struct {
    Name, Description string
    Level  string
    Model  string   // "sonnet" / "opus" / "haiku"
    Skills []string // fallback if no skill-grants.yaml entry matches
    Tools  []string // additional tools beyond the base set
    Body   string   // markdown body after YAML frontmatter (system prompt content)
}
```

## Lifecycle

### Phase 1 — Startup: Plugin Cloning (`loadManifestPlugins`)

Called once during `main()` before the SDK session is created. Returns a list of local paths to pass to `WithLocalPlugin`.

1. **Read manifest**: `loadManifestPlugins(statePath)` reads `state/system/plugins.yaml`. Parse errors are logged and the function returns nil (non-fatal).

2. **Resolve cache dir**: `pluginCacheDir = ~/.cache/bud/plugins/` (via `os.UserCacheDir()`).

3. **For each entry** in `manifest.Plugins`:
   - **Local path** (`path:` field set, no `owner`): skip git entirely; call `resolvePluginPathsFromLocalPath(e.localPath)` directly.
   - **Remote repo (first run)**: if `~/.cache/bud/plugins/<owner>/<repo>/` does not exist, run `git clone --depth=1 [--branch <ref>] <url> <repoDir>`.
   - **Remote repo (subsequent runs)**:
     - If `e.ref` is set (pinned): `git fetch --depth=1 origin <ref>` then `git checkout <ref>`.
     - If `e.ref` is empty (floating): `git pull --ff-only`. Failures are logged but non-fatal — existing checkout is used.

4. **Resolve plugin dirs**: Apply `e.dir` to get `localPath = repoDir[/dir]`. Call `resolvePluginPathsFromLocalPath(localPath)`:
   - If `localPath` itself has `.claude-plugin/plugin.json` → return as single dir.
   - Otherwise, scan immediate subdirectories and return those that `looksLikePluginDir()` (has `.claude-plugin/plugin.json`, an `agents/` dir, or `.md` files). Handles monorepo-style repos with multiple plugins.

5. **Return paths** — passed to the Claude Agent SDK as local plugin directories via `WithLocalPlugin`.

### Phase 2 — Startup: MCP Tool Registration & `SetKnownMCPTools`

After all MCP tools are registered (including stdio proxy servers from `.mcp.json`), `main.go` calls:

```go
exec.SetKnownMCPTools(tools)
```

This stores the full list of live MCP tool names on the executive. These names are required for wildcard expansion in tool grants (e.g., `mcp__bud2__gk_*` → all registered gk tools). **This call must happen before any agent is spawned.**

### Phase 3 — Per-Prompt: Plugin Dir Resolution & Exclude Filtering (`resolvedManifestPluginDirs`)

Called inside `SendPrompt` (via `allPluginDirsForAgents`) on every prompt, with no git operations. This is the hot-path read of the already-cloned plugins.

For each entry in `plugins.yaml`:
1. Resolve `localPath` (same logic as Phase 1 but without git ops).
2. Build `excludeSet` from `e.Exclude` (a `map[string]bool` keyed by directory basename).
3. Call `resolvePluginPathsFromLocalPath(localPath)` to enumerate plugin dirs.
4. **Filter**: skip any `p` where `filepath.Base(p)` is in `excludeSet`; log the skip.
5. Append surviving dirs as `pluginDir{Path: p, ToolGrants: e.ToolGrants}`.

The `exclude:` field therefore takes effect at agent-load time, not at clone time. A restart is not needed to add or remove entries from the exclude list.

### Phase 4 — Per-Prompt: Zettel Library Generation (`generateZettelLibraries`)

Called inside `SendPrompt` immediately after `WithLocalPlugin` is set up:

```go
generateZettelLibraries(s.statePath)
```

1. **Seed home library**: prepend `{Name: "home", Path: state/zettels, Default: true}`.
2. **Compute cache base**: `os.UserCacheDir()` → `pluginCacheDir = ~/.cache/bud/plugins/`.
3. **Scan all plugin dirs** via `allPluginDirs(statePath)` (merges local + manifest plugins).
4. For each dir: read `.claude-plugin/plugin.json`. If `"zettels"` key is present:
   - Use `manifest.Name` as library name (falls back to `filepath.Base(dir)`).
   - Compute `zettelsPath = filepath.Join(dir, manifest.Zettels)`.
   - Set `Readonly = manifest.Readonly || strings.HasPrefix(dir, pluginCacheDir)` — cache clones are always readonly.
5. Marshal to `state/system/zettel-libraries.yaml` with a `# Generated` header comment.

The file is regenerated on every prompt, so plugin zettel declarations are picked up without restart.

### Phase 5 — Agent Spawn: `LoadAllAgents` / `ResolveSubagentConfig`

When the executive spawns a subagent, it calls either `LoadAllAgents` (batch) or `ResolveSubagentConfig` (single on-demand):

**`LoadAllAgents(statePath, knownTools)`** (`agent_defs.go`):

1. **Load aliases**: `LoadAgentAliases(statePath)` reads `agent-aliases.yaml`.
2. **Load skill grants**: `LoadSkillGrants(statePath)` reads `skill-grants.yaml`. Returns empty grants if file missing.
3. **Collect plugin dirs**: `allPluginDirsForAgents(statePath)` merges:
   - Local plugins: `scanLocalPlugins()` scans `state/system/plugins/` — each gets `pluginDir{ToolGrants: nil}`.
   - Manifest plugins: `resolvedManifestPluginDirs()` returns already-cloned dirs with exclude filtering applied and `ToolGrants` intact.
4. For each plugin dir, scan `agents/` for `*.md` and `*.yaml` files. For each agent:
   a. Parse via `parseAgentData()` (YAML frontmatter + markdown body).
   b. Build agent key: `"<pluginNamespace>:<agentName>"`.
   c. **Resolve skills**: `resolveGrantedSkills(grants, key)` — match against skill-grants.yaml; fallback to `agent.Skills`.
   d. **Assemble prompt**: load skill content via `LoadSkillContent(pluginDirs, skillName)` and concatenate to agent body.
   e. **Expand tool grants**: for each manifest entry's `ToolGrants`, find patterns matching the agent key via `matchesAgentPattern()`, then call `expandToolGrants(grantedTools, knownTools)` to expand wildcards.
   f. Build `claudecode.AgentDefinition` with assembled prompt and merged tool list.
5. Return the agent definition map.

### Phase 6 — Skill Resolution (`resolveGrantedSkills`)

Priority order (first match wins):
1. **Exact match**: `grants["autopilot-vision:planner"]`
2. **Namespace wildcard**: `grants["autopilot-vision:*"]`
3. **Glob patterns**: `filepath.Match("autopilot-*:planner", key)` for all non-exact, non-`*` patterns
4. **Global wildcard**: `grants["*"]`

If none match, returns `(nil, false)` — the agent's own `Skills` field is used as fallback.

## Design Decisions

- **Startup cloning, on-demand agent loading**: `loadManifestPlugins` (with git ops) runs once at startup. `resolvedManifestPluginDirs` (no git, read-only) is called whenever agent definitions are needed. This avoids git operations on the hot path while keeping plugin content current.

- **`exclude:` at load time, not clone time**: The exclude list is evaluated by `resolvedManifestPluginDirs`, not during the initial git clone. This means you can add or remove sub-plugins from consideration (e.g., disable `issues-linear`) just by editing `plugins.yaml` — no restart required for the filter change to apply.

- **Zettel libraries regenerated on every prompt**: `generateZettelLibraries` runs inside `SendPrompt` so new plugins' zettel declarations take effect on the next wake without daemon restart. The tradeoff is a small file I/O cost per prompt.

- **Cache clones are always readonly in zettel-libraries**: Any plugin dir under the OS cache path (`~/.cache/bud/plugins/`) is marked `readonly: true` in `zettel-libraries.yaml` regardless of what `plugin.json` says. This prevents accidental commits into externally-managed checkouts.

- **Tool grants live in plugins.yaml, not skill-grants.yaml**: Plugin authors control which MCP tools their agents can use via `tool_grants` in the manifest. Core configuration (`skill-grants.yaml`) controls which skills agents get. This separates external trust grants from internal skill assignment.

- **Wildcard expansion against live tool list**: `expandToolGrants` resolves patterns like `mcp__bud2__gk_*` against registered MCP tool names at agent-load time. When a new gk tool is added to the MCP server, any agent with a matching wildcard grant automatically gets access — no manifest change required.

- **Local plugins (`state/system/plugins/`) cannot have tool grants**: Only manifest entries (in `plugins.yaml`) carry `ToolGrants`. Agents in local plugin dirs can only access tools declared in their `tools:` YAML field.

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `cmd/bud/main.go` | `executive.loadManifestPlugins` | Called at startup; returns resolved plugin paths passed to SDK's `WithLocalPlugin` |
| `cmd/bud/main.go` | `executive.SetKnownMCPTools` | Called after all MCP tools registered; provides the name list for wildcard expansion |
| `internal/executive/simple_session.go:SendPrompt` | `generateZettelLibraries` | Called each prompt invocation; writes `state/system/zettel-libraries.yaml` |
| `internal/executive/executive_v2.go` | `agent_defs.LoadAllAgents` | Called when rebuilding the SDK session's agent pool (triggered by `SetKnownMCPTools`) |
| `internal/executive/agent_defs.go` | `simple_session.allPluginDirsForAgents` | Provides `[]pluginDir` list (local + manifest with grants + exclude applied) for agent scanning |
| `internal/executive/agent_defs.go` | `profiles.LoadSkillGrants` + `LoadSkillContent` | Loads skill grant table and reads SKILL.md files from plugin dirs |
| `internal/executive/profiles.go:ResolveSubagentConfig` | `profiles.LoadAgent` + `LoadSkillContent` | Single-agent path used by the executive for on-demand spawning with explicit agent name |

## Non-Obvious Behaviors

- **Exclude filtering happens in `resolvedManifestPluginDirs`, not `loadManifestPlugins`**: The startup cloning phase (`loadManifestPlugins`) clones every sub-plugin regardless of `exclude:`. The exclude list is only consulted later, in `resolvedManifestPluginDirs`, when building the `pluginDir` list for agent loading. Excluded repos are still cloned and kept up to date on disk.

- **Zettel-libraries regeneration means plugin.json changes take effect without restart**: Because `generateZettelLibraries` runs inside `SendPrompt` on every prompt invocation, adding a `"zettels"` key to a plugin's `plugin.json` (in a local-path plugin) is picked up on the next Bud wake, not just on daemon restart.

- **A single manifest entry can expand to many plugin dirs**: If `useful-plugins` is listed with no `dir:`, and it contains `dev-docs/`, `bud-ops/`, etc., each becomes a separate `pluginDir` inheriting the same `ToolGrants` from that manifest entry. Combine this with `exclude:` to selectively suppress individual sub-plugins.

- **Tool grants are not applied during `loadManifestPlugins`**: The cloning phase returns only plain paths. Tool grants are read separately by `resolvedManifestPluginDirs()` when building agent definitions. If you change a `tool_grants:` entry in plugins.yaml while bud is running, the change is picked up on the next agent-load without restart.

- **`skill-grants.yaml` absence is non-fatal**: If the file doesn't exist, `LoadSkillGrants` returns empty grants and every agent falls back to its own `skills:` field. The system works as before the file was introduced — backward compatible.

- **`SetKnownMCPTools` triggers a full agent reload**: After storing the tool list, `executive_v2.go` immediately calls `LoadAllAgents`. Any tool grants with wildcards that couldn't be expanded before (because the list was empty) are now fully expanded.

- **Default statePath is now `~/Documents/bud-state`**: Since commit `cb3340c`, `STATE_PATH` env defaults to `filepath.Join(os.UserHomeDir(), "Documents", "bud-state")` rather than the relative path `"state"`. This affects any code or script that expected the old default.

## Start Here

- `internal/executive/simple_session.go` — read `loadManifestPlugins`, `resolvedManifestPluginDirs`, `generateZettelLibraries`, `expandToolGrants`, and `matchesAgentPattern` to understand the full plugin discovery, exclude filtering, grant expansion, and zettel generation pipeline
- `state/system/plugins.yaml` — the live manifest driving cloning, dir resolution, `exclude:` filtering, and tool grant declarations for external plugins
- `internal/executive/agent_defs.go` — `LoadAllAgents` is the integration point combining plugin dirs, skill grants, and tool grants into `claudecode.AgentDefinition` objects
- `state/system/skill-grants.yaml` — centralized skill assignment; the `"bud:*": []` and `"*":` entries reveal override precedence
- `internal/executive/profiles.go` — `resolveGrantedSkills`, `LoadSkillContent`, `Agent` type, and `AgentAliases` for understanding how skill names resolve to file content
