# Bud Plugin Directory

Bud loads plugins from any directory via `--plugin-dir`. Each plugin is a folder containing `agents/` and/or `skills/` subdirectories.

## Core Plugins (bundled with Bud)

These ship in `seed/plugins/` and are always available:

- **bud** — Core Bud agents (coder, researcher, reviewer, writer)
- **bud-ops** — Bud operational skills (handle-subagent-complete, things-operations, etc.)

## First-Party Plugins

Maintained by the Bud project, optional:

- **[useful-plugins](https://github.com/vthunder/useful-plugins)** — General-purpose plugins:
  - `zettel` — Zettelkasten knowledge management skills
  - `dev-docs` — Documentation generation and maintenance (arch-doc, doc-scan, repo-doc, etc.)
  - `dev-general` — Development skills (code-review, web-research, prd)

- **[autopilot](https://github.com/vthunder/autopilot)** — Autonomous planning cascade (vision → strategy → epic → task)
  - Clone the repo, then add `--plugin-dir ~/src/autopilot/plugins` to your Bud config

## Project-Specific Plugins

These live in their own project repos:

- **sandmill** — Skills for managing the sandmill.org blog and Mac OS 8 emulator (in the sandmill repo)

## Installing a Plugin

Clone the repo containing the plugin, then add it to your Bud startup config:

```
--plugin-dir /path/to/plugin-repo/plugins
```

Bud will load all plugin directories found under the specified path.
