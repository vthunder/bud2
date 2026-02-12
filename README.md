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

| What | Command |
|------|---------|
| Build everything | `./scripts/build.sh` |
| Restart daemon | `launchctl kickstart -k gui/501/com.bud.daemon` |
| Check daemon status | `launchctl list | grep bud` |
| View logs | `tail -f ~/Library/Logs/bud.log` |
| Test state server | `curl http://localhost:3100/health` |

### Project Structure

```
bud2/
├── bin/                    # Compiled binaries (gitignored)
├── daemon.ts              # Main daemon entrypoint
├── scripts/               # Build and utility scripts
├── state/                 # Working directory for Bud
│   ├── system/           # System configuration and guides
│   │   ├── guides/       # Detailed reference docs
│   │   ├── memory.db     # Long-term memory
│   │   └── activity.jsonl # Activity log
│   ├── notes/            # Research and documentation
│   └── projects/         # Active projects
└── deploy/               # Deployment configuration
```

### Configuration

- **Launchd plist:** `~/Library/LaunchAgents/com.bud.daemon.plist`
- **Entrypoint script:** `deploy/run-bud.sh`
- **State directory:** `state/` (working directory for Bud)
- **Logs:** `~/Library/Logs/bud.log`

## Development Workflow

1. **Make changes** to daemon code
2. **Build:** `./scripts/build.sh`
3. **Restart:** `launchctl kickstart -k gui/501/com.bud.daemon`
4. **Verify:** `tail -f ~/Library/Logs/bud.log`

## Documentation

Detailed guides are in `state/system/guides/`:

- [**repositories.md**](state/system/guides/repositories.md) - Git workflow, branching, PRs
- [**state-management.md**](state/system/guides/state-management.md) - Memory, sessions, introspection
- [**projects.md**](state/system/guides/projects.md) - Project folders and notes
- [**gtd.md**](state/system/guides/gtd.md) - Task management
- [**integrations.md**](state/system/guides/integrations.md) - Notion, Calendar, GitHub
- [**observability.md**](state/system/guides/observability.md) - Activity logs and journals
- [**wellness.md**](state/system/guides/wellness.md) - Daily housekeeping

## Architecture

Bud runs as a macOS launchd service:
- **Daemon** (`bin/bud`) runs continuously, managing memory and autonomous work
- **State server** (`bin/bud-state`) provides MCP tools for memory/state access
- **Background jobs** (consolidation, compression) run periodically
- **Claude Code integration** via MCP protocol

The daemon operates in `state/` as its working directory, maintaining continuity across sessions.
