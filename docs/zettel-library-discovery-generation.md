---
topic: Zettel Library Discovery & Generation
repo: bud2
generated_at: 2026-04-08T10:00:00Z
commit: 57afbdb
key_modules: [internal/executive/simple_session.go, cmd/bud/main.go]
score: 0.48
---

# Zettel Library Discovery & Generation

> Repo: `bud2` | Generated: 2026-04-08 | Commit: 57afbdb

## Summary

At startup, bud scans every loaded plugin directory for `plugin.json` files that declare a `"zettels"` path and writes a consolidated `state/system/zettel-libraries.yaml`. This file acts as a registry that tells Claude which zettel libraries exist, where they live, and whether they are writable. Plugins cloned from the OS cache directory (GitHub remotes) are always marked readonly to prevent accidental commits into managed checkouts.

## Key Data Structures

### `zettelLibrary` (`internal/executive/simple_session.go`)
Represents a single library entry in the generated YAML file.
```go
type zettelLibrary struct {
    Name     string `yaml:"name"`
    Path     string `yaml:"path"`
    Default  bool   `yaml:"default,omitempty"`
    Readonly bool   `yaml:"readonly,omitempty"`
}
```
- `Name` comes from `plugin.json`'s `"name"` field (or the directory basename as fallback).
- `Path` is the absolute path to the `"zettels"` subdirectory declared in the manifest.
- `Readonly` is forced `true` for any plugin loaded from the OS cache path (`~/Library/Caches/bud/plugins/`), regardless of what the manifest declares.

### `zettelLibrariesFile` (`internal/executive/simple_session.go`)
Top-level serialization wrapper.
```go
type zettelLibrariesFile struct {
    Libraries []zettelLibrary `yaml:"libraries"`
}
```
Written to `<statePath>/system/zettel-libraries.yaml` by `generateZettelLibraries`.

### `pluginDir` (`internal/executive/simple_session.go`)
Associates a local filesystem path with the tool grants from its manifest entry. Used as the enumeration unit when discovering plugins.
```go
type pluginDir struct {
    Path       string
    ToolGrants map[string][]string
}
```

## Lifecycle

1. **Startup wiring** (`cmd/bud/main.go`, `main()`): After the state directory is created and seed dirs are seeded, `main.go` calls `generateZettelLibraries(statePath)` unconditionally before the executive is initialized. This ensures the file exists before any Claude session starts.

2. **Plugin directory enumeration** (`generateZettelLibraries` in `simple_session.go`): The function calls `allPluginDirs(statePath)` which combines two sources:
   - **Local plugins**: directories under `state/system/plugins/` whose subdirectories pass `looksLikePluginDir` (has `.claude-plugin/plugin.json`, an `agents/` subdir, or `.md` files).
   - **Manifest plugins**: directories in `~/Library/Caches/bud/plugins/` cloned from `state/system/plugins.yaml` entries, resolved via `resolvePluginPathsFromLocalPath` (monorepo entries are expanded one level deep).

3. **Plugin manifest inspection**: For each discovered plugin directory, `generateZettelLibraries` attempts to read `<dir>/.claude-plugin/plugin.json`. If the file is absent or the JSON has no `"zettels"` field, the plugin is silently skipped.

4. **Readonly determination**: The plugin path is compared against the OS user cache directory (`os.UserCacheDir()`). Any path rooted under the cache directory (i.e., a GitHub-cloned plugin) has `Readonly` forced to `true`, overriding whatever the `plugin.json` declares.

5. **YAML write**: The collected `[]zettelLibrary` slice is serialized with `gopkg.in/yaml.v3` and written atomically to `<statePath>/system/zettel-libraries.yaml`. If the directory does not exist or the write fails, the error is logged but startup continues — the file is treated as optional by the rest of the system.

6. **Consumption**: The generated file is not read by bud directly after startup. It is placed in `state/system/` so it is visible to Claude sessions (which run with `WorkDir = statePath`) and to any MCP tools or skills that need to enumerate zettel libraries (e.g., a zettel-writing skill that checks which library to write to).

## Design Decisions

- **Generated at startup, not on demand**: `generateZettelLibraries` runs once in `main()`, not lazily on first use. This keeps the file always present when the executive starts and avoids concurrency issues with multiple sessions discovering the same plugins in parallel.

- **Cache-cloned plugins are always readonly**: The OS cache directory rule enforces a hard invariant: Bud never commits user-created zettel content into GitHub-managed plugin checkouts. Local path entries (`path:` in `plugins.yaml`) are not in the cache directory and therefore can be writable. This is a security boundary — it prevents zettel writes from silently modifying a shared upstream repo.

- **Monorepo expansion is one level deep** (`resolvePluginPathsFromLocalPath`): A cloned repo root that doesn't look like a plugin dir is expanded into its immediate subdirectories. This supports monorepo-style plugins where multiple plugins live under one GitHub repo. Only one level is expanded — deeper nesting is not supported.

- **Silent skip on missing `plugin.json`**: The function treats absent or malformed manifests as "this plugin has no zettels" rather than an error. This matches the general startup philosophy of bud: individual plugin failures are logged and skipped so the daemon starts regardless.

- **No deduplication by path**: If two plugin directories declare the same absolute `"zettels"` path, both will appear in the YAML. This is a latent bug but has not been observed in practice since each plugin typically owns a distinct zettels directory.

## Integration Points

| From | To | What crosses the boundary |
|------|----|--------------------------|
| `cmd/bud/main.go` | `internal/executive/simple_session.go` | Calls `generateZettelLibraries(statePath)` at startup |
| `generateZettelLibraries` | `allPluginDirs(statePath)` | Receives the full list of plugin directory paths (local + manifest-cloned) |
| `allPluginDirs` | `resolvePluginPathsFromLocalPath` | Expands manifest plugin cache paths into per-plugin dirs |
| `generateZettelLibraries` | `state/system/zettel-libraries.yaml` | Writes the generated YAML file (consumed by Claude sessions and skills) |
| `generateZettelLibraries` | `state/system/plugins/*/` | Reads `.claude-plugin/plugin.json` from local plugin dirs |
| `generateZettelLibraries` | `~/Library/Caches/bud/plugins/*/` | Reads `.claude-plugin/plugin.json` from cached remote plugin dirs |

## Non-Obvious Behaviors

- **The file is always overwritten**: Every startup regenerates `zettel-libraries.yaml` from scratch. Any manual edits to the file will be silently lost on the next restart. If you need a custom library, add it via a local plugin `path:` entry in `plugins.yaml`.

- **`Readonly` in `plugin.json` is ignored for cache plugins**: A plugin manifest can declare `"readonly": false`, but if the plugin lives in the OS cache directory, bud overrides it to `true`. The only way to get a writable cache-cloned plugin would be to move the checkout out of the cache — i.e., use a `path:` entry.

- **Generation happens before the executive is initialized**: `generateZettelLibraries` is called before `executive.NewExecutiveV2(...)`. If the executive reads `zettel-libraries.yaml` at construction time (likely via the MCP config or seed files), the file is already current. There is no race between generation and first use.

- **Plugin discovery uses `allPluginDirs`, not `allPluginDirsForAgents`**: The zettel generation function does not need tool grant information, so it uses the simpler `allPluginDirs` helper. Agent-aware loading (which carries grants) uses `allPluginDirsForAgents` — a separate function that is only called when building agent definitions.

- **`looksLikePluginDir` is a heuristic**: A directory qualifies as a plugin if it has `.claude-plugin/plugin.json`, an `agents/` subdir, or `.md` files. This means a directory with only markdown files (e.g., a documentation repo with a `path:` entry) is considered a plugin dir. In practice, such a dir will be skipped by `generateZettelLibraries` because it won't have a `plugin.json`.

## Start Here

- `internal/executive/simple_session.go` — contains `generateZettelLibraries`, `zettelLibrary`, `zettelLibrariesFile`, `allPluginDirs`, and `resolvePluginPathsFromLocalPath`; this is the entire implementation
- `cmd/bud/main.go` — shows the call site (`generateZettelLibraries(statePath)`) and its position in the startup sequence relative to seed initialization and executive construction
- `state/system/plugins.yaml` — the manifest that drives which external repos are cloned; `path:` entries here produce writable zettel library candidates
- `~/.cache/bud/plugins/` (or `~/Library/Caches/bud/plugins/` on macOS) — where GitHub-cloned plugins land; all zettel libs from this directory are readonly
- Any `seed/plugins/*/` or `state/system/plugins/*/` directory with a `.claude-plugin/plugin.json` that includes a `"zettels"` key — that is the minimal hook to add a new zettel library
