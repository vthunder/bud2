# Working with Code Repositories

This guide covers how I work with source code repositories.

## Repository Locations

Repositories live in `~/src/`. When working on a project, I operate within that directory.

Examples:
- `~/src/bud2` - this project
- `~/src/other-project` - other projects

## Making Changes

### Branch Workflow

1. **Check current state**: `git status`, `git branch`
2. **Create feature branch** (for non-trivial changes): `git checkout -b feature/description`
3. **Make changes**: edit files, run tests
4. **Commit**: with clear messages explaining the "why"
5. **Push**: `git push -u origin branch-name`

### When to Branch

- **Direct to main**: Trivial fixes, documentation, small self-contained changes
- **Feature branch + PR**: New features, refactors, anything that benefits from review

## Pull Requests

### Creating PRs

Use `gh pr create` with:
- Clear title describing the change
- Summary of what and why
- Test plan if applicable

### Merging PRs

**IMPORTANT**: I do NOT merge PRs without explicit owner approval.

Workflow:
1. Create PR
2. Notify owner (via talk_to_user)
3. Wait for review/approval
4. Owner merges, or owner grants permission to merge

Even if tests pass and the PR looks good, I wait for human approval before merging.

## Multi-Repository Work

When work spans multiple repositories:
1. Track the overall goal in beads
2. Create separate PRs for each repo
3. Note dependencies between PRs
4. Coordinate merging order with owner

## Commit Messages

Format:
```
type: short description

Longer explanation if needed.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
```

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`

## Building and Deployment

### Building Binaries

**ALWAYS use the build script**:
```bash
~/src/bud2/scripts/build.sh
```

This builds all binaries:
- `bin/bud` - main daemon
- `bin/bud-state` - state management server
- `bin/efficient-notion-mcp` - Notion MCP server
- `bin/compress-episodes` - episode compression
- `bin/compress-traces` - trace compression
- `bin/consolidate` - memory consolidation

**Never guess build commands** - always use `scripts/build.sh`.

### Restarting the Daemon

Bud runs as a launchd service. After rebuilding:

```bash
launchctl kickstart -k gui/$(id -u)/com.bud.daemon
```

Or the shorthand:
```bash
launchctl kickstart -k gui/501/com.bud.daemon
```

**Why this matters**: Changes to the daemon code only take effect after restart. The running daemon won't see code changes until it's restarted.

### Using trigger_bud_redeploy MCP Tool

The `trigger_bud_redeploy` MCP tool kicks off a full deploy (git pull + build + restart).

**MANDATORY RULE: Always announce before deploying.**

Before calling `trigger_bud_redeploy`, I MUST call `talk_to_user` with:
1. What I'm deploying
2. Why (the specific reason/change)

Example:
> "Deploying now: adding `idx_trace_sources_episode` index to fix the 400ms `get_unconsolidated` query bottleneck."

**Never call `trigger_bud_redeploy` silently.** The restart will cause session death, and the next session won't know what happened. The announcement is the only durable record visible to the user.

This applies even during autonomous wakes — announce first, then deploy.

### Pre-Deploy Checklist

When redeploying with subagents potentially running, follow this checklist to ensure continuity:

1. **Run the redeploy-bud job** to save subagent state:
   ```
   Agent_spawn_async(job="redeploy-bud")
   ```

2. **Read the job output** — it will include:
   - How many subagents were saved
   - The exact announcement text to send

3. **Announce to the user** using the text from the job output:
   ```
   talk_to_user("Deploying now: [reason]. N subagent(s) will be restarted after startup.")
   ```

4. **Call trigger_bud_redeploy** — after the announcement, proceed with the deploy.

After the new session starts, a startup impulse will automatically trigger the `startup` job, which reads the restart notes and re-spawns any interrupted subagents.

---

### Deployment Workflow (Manual)

**IMPORTANT: deploy.sh blocks for 60-90s. Sessions die mid-deploy. Use this protocol:**

1. **Make code changes** in `~/src/bud2`, commit to git
2. **Run deploy in background**:
   ```bash
   nohup ./deploy/deploy.sh > /tmp/bud-deploy.log 2>&1 &
   echo "Deploy started (pid: $!). Check /tmp/bud-deploy-success for completion."
   ```
3. **Immediately send a message** to the user: "Deploy started in background. Will complete in ~60-90s."
4. **Do NOT wait** for deploy to finish — the session will die before it completes
5. **Next session verifies**: `cat /tmp/bud-deploy-success` shows the completion timestamp

**Checking deploy status:**
```bash
# Did deploy succeed? (shows timestamp if yes, error if not yet)
cat /tmp/bud-deploy-success

# What's in the deploy log?
tail -20 /tmp/bud-deploy.log

# Is the new binary running?
ps aux | grep bud
```

**Why this protocol:**
- deploy.sh takes 60-90s (git pull + go build + launchctl restart)
- Claude Code sessions time out or get killed before it finishes
- The session death leaves NO trace in the episode log
- Next session sees stale state and thinks deploy didn't happen → deploy loop

**Alternative for manual deploy:**
```bash
# Quick build + restart without git pull
~/src/bud2/scripts/build.sh && launchctl kickstart -k gui/$(id -u)/com.bud.daemon
```

### Launchd Configuration

**bud daemon** runs via launchd plist at:
- `~/Library/LaunchAgents/com.bud.daemon.plist`
- Executes `/Users/thunder/src/bud2/deploy/run-bud.sh`
- Runs at startup (`RunAtLoad`)
- Keeps alive on crash (`KeepAlive`)
- Logs to `~/Library/Logs/bud.log`

**Engram** also runs via launchd:
- `~/Library/LaunchAgents/com.bud.engram.plist`
- Executes `/Users/thunder/src/engram/engram` directly
- Runs at startup (`RunAtLoad`), keeps alive on crash
- Logs to `~/Library/Logs/engram.log`
- To restart after rebuild: `launchctl kickstart -k gui/$(id -u)/com.bud.engram`
- To rebuild and restart Engram: `cd ~/src/engram && go build . && launchctl kickstart -k gui/$(id -u)/com.bud.engram`

## MCP Server Changes

When modifying MCP server code (e.g., `efficient-notion-mcp`):

1. **Make changes** in the source repo
2. **Commit and push** to remote
3. **Update dependency** in bud2: `go get github.com/vthunder/efficient-notion-mcp@latest`
4. **Rebuild**: `~/src/bud2/scripts/build.sh`
5. **Restart bud**: `launchctl kickstart -k gui/501/com.bud.daemon`

**Common mistake**: Forgetting step 5. The MCP server binary is rebuilt, but the running instance is still the old code. Always restart after MCP changes.

## Safety

- Never force push to main/master
- Never commit secrets or credentials
- Run tests before committing
- Keep commits atomic and reversible
