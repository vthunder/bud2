# Bud - Personal AI Agent

Bud is a personal AI agent that runs as a macOS daemon, providing memory, task management, and autonomous assistance.

## Quick Reference

### Build & Deploy

**Build all binaries:**
```bash
./scripts/build.sh
```

**Restart the daemon** (after code changes):
```bash
launchctl kickstart -k gui/501/com.bud.daemon
```

**View logs:**
```bash
tail -f ~/Library/Logs/bud.log
```

### Common Tasks

**macOS (launchd):**

| What | Command |
|------|---------|
| Build everything | `./scripts/build.sh` |
| Restart daemon | `launchctl kickstart -k gui/$(id -u)/com.bud.daemon` |
| Check daemon status | `launchctl list \| grep bud` |
| View logs | `tail -f ~/Library/Logs/bud.log` |

**Linux (systemd):**

| What | Command |
|------|---------|
| Build everything | `./scripts/build.sh` |
| Restart daemon | `systemctl --user restart bud.service` |
| Check daemon status | `systemctl --user status bud.service` |
| View logs | `journalctl --user -u bud -f` |

### Project Structure

```
bud2/
├── cmd/                       # Go entrypoints
│   ├── bud/                  # Main daemon (main.go, debug_executive.go)
│   ├── efficient-notion-mcp/ # Notion MCP server
│   ├── sdk-harness/          # SDK test harness
│   ├── sdk-verify/           # SDK verification tool
│   └── test-synthetic/       # Synthetic test runner
├── internal/                  # Go packages (core logic)
│   ├── executive/            # Executive decision engine & session management
│   ├── engram/               # Memory service client (long-term graph memory)
│   ├── reflex/               # Automated reflex engine
│   ├── focus/                # Attention & salience system
│   ├── gtd/                  # Task management
│   ├── mcp/                  # MCP server & tool registrations
│   ├── config/               # Multi-provider LLM configuration
│   ├── senses/               # Input adapters (Discord, calendar)
│   ├── effectors/            # Output adapters (Discord sending)
│   ├── memory/               # Short-term working memory (percepts, threads, traces)
│   ├── integrations/         # External service helpers (calendar, GitHub)
│   ├── budget/               # Token/thinking-time budget tracking
│   ├── embedding/            # Embedding generation
│   ├── eval/                 # Evaluation utilities
│   ├── logging/              # Structured logging
│   ├── paths/                # Path resolution
│   ├── profiling/            # Profiling support
│   ├── state/                # State management helpers
│   ├── tmux/                 # Tmux integration
│   ├── zellij/               # Zellij integration
│   ├── activity/             # Activity logging
│   └── types/                # Shared type definitions
├── seed/                      # Template files (seeded to state/ on startup)
│   └── system/               # System templates & configuration
│       ├── core.md           # Core system prompt
│       ├── startup-instructions.md  # Startup impulse instructions
│       ├── wakeup.md         # Autonomous wake instructions
│       ├── guides/           # Reference docs (GTD, reflexes, projects, etc.)
│       ├── reflexes/         # Reflex YAML definitions
│       ├── plugins/          # Core plugin bundles (bud, bud-ops)
│       ├── profiles/         # Agent profile definitions
│       ├── jobs/             # Background job definitions
│       ├── workflows/        # Workflow definitions
│       └── agent-aliases.yaml # Agent alias configuration
├── deploy/                    # Deployment config (launchd, systemd, scripts)
├── scripts/                   # Build, test, and utility scripts
├── tests/                     # Integration test scenarios
├── bin/                       # Compiled binaries (gitignored)
├── things-mcp/                # Things 3 MCP integration (TypeScript)
├── sidecar/                   # Sidecar services (NER extraction)
├── docs/                      # Architecture and design docs
└── state/                     # Runtime state directory (gitignored, separate repo)
```

### Configuration

- **Entrypoint script:** `deploy/run-bud.sh`
- **State directory:** `state/` (working directory for Bud)
- **macOS service:** `~/Library/LaunchAgents/com.bud.daemon.plist`
- **macOS logs:** `~/Library/Logs/bud.log`
- **Linux service:** `~/.config/systemd/user/bud.service`
- **Linux logs:** `~/.local/state/bud/bud.log`

### Model Configuration

Copy `bud.yaml.example` to `bud.yaml` and set your models:

```yaml
providers:
  claude-code:
    type: claude-code
    models:
      claude-code/claude-sonnet-4-6:
        context_window: 200000
      claude-code/claude-opus-4-6:
        context_window: 200000

models:
  executive: claude-code/claude-sonnet-4-6  # main session model
  agent: claude-code/claude-sonnet-4-6      # subagent model
```

`bud.yaml` is gitignored. `bud.yaml.example` is the reference. The `claude-code` provider uses your Claude Code CLI auth — no separate API key required. Switch `executive` to `claude-code/claude-opus-4-6` for higher quality reasoning.

## Development Workflow

1. **Make changes** to Go source in `cmd/` or `internal/`
2. **Build:** `./scripts/build.sh`
3. **Restart:** `launchctl kickstart -k gui/$(id -u)/com.bud.daemon` (macOS) or `systemctl --user restart bud.service` (Linux)
4. **Verify:** Check logs (see Common Tasks above)

## Documentation

Detailed guides are in `seed/system/guides/`:

- [**repositories.md**](seed/system/guides/repositories.md) - Git workflow, branching, PRs
- [**state-management.md**](seed/system/guides/state-management.md) - Memory, sessions, introspection
- [**projects.md**](seed/system/guides/projects.md) - Project folders and notes
- [**gtd.md**](seed/system/guides/gtd.md) - Task management
- [**integrations.md**](seed/system/guides/integrations.md) - Notion, Calendar, GitHub
- [**observability.md**](seed/system/guides/observability.md) - Activity logs and journals
- [**wellness.md**](seed/system/guides/wellness.md) - Daily housekeeping

**Integrations:**
- [**Things Integration**](docs/things-integration.md) - Use Things 3 as your GTD backend (set `USE_THINGS=true`)

## Architecture

Bud runs as a system service (launchd on macOS, systemd on Linux):
- **Daemon** (`bin/bud`) runs continuously, managing memory and autonomous work
- **Background jobs** (consolidation, compression) run periodically
- **Claude Code integration** via MCP protocol

The daemon operates in `state/` as its working directory, maintaining continuity across sessions.
