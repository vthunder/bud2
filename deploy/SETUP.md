# Bud Deployment Setup for Mac Mini

## Prerequisites

On the Mac Mini, install:

```bash
# Homebrew (if not installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Go
brew install go

# fswatch (for redeploy watcher)
brew install fswatch

# Claude CLI
npm install -g @anthropic-ai/claude-code
claude  # Run once to authenticate
```

## Initial Setup

1. **Clone the repo** on Mac Mini:
   ```bash
   cd ~
   git clone https://github.com/vthunder/bud2.git
   mkdir -p ~/bud2/bin
   ```

2. **Create .env file**:
   ```bash
   cp ~/bud2/.env.example ~/bud2/.env
   # Edit with your Discord token, channel ID, etc.
   ```

3. **Update plist files** - replace `YOU` with your username:
   ```bash
   cd ~/bud2/deploy
   sed -i '' "s|/Users/YOU|$HOME|g" com.bud.daemon.plist
   sed -i '' "s|/Users/YOU|$HOME|g" com.bud.watcher.plist
   ```

4. **Initial build**:
   ```bash
   cd ~/bud2
   go build -o bin/bud ./cmd/bud
   go build -o bin/bud-mcp ./cmd/bud-mcp
   ```

5. **Install launchd services**:
   ```bash
   # Copy plists to LaunchAgents
   cp ~/bud2/deploy/com.bud.daemon.plist ~/Library/LaunchAgents/
   cp ~/bud2/deploy/com.bud.watcher.plist ~/Library/LaunchAgents/

   # Load services
   launchctl load ~/Library/LaunchAgents/com.bud.daemon.plist
   launchctl load ~/Library/LaunchAgents/com.bud.watcher.plist
   ```

6. **Verify it's running**:
   ```bash
   launchctl list | grep bud
   tail -f ~/Library/Logs/bud.log
   ```

## Deploying from Laptop

Add to your laptop's `~/.ssh/config`:
```
Host mini
    HostName your-mac-mini.local
    User yourusername
```

Then deploy with:
```bash
ssh mini "~/bud2/deploy/deploy.sh"
```

Or create a local alias:
```bash
alias deploy-bud='ssh mini "~/bud2/deploy/deploy.sh"'
```

## Self-Redeploy (Bud triggers its own redeploy)

Bud can request a redeploy by touching the trigger file:

```go
// In bud code or via MCP tool
os.WriteFile("/tmp/bud-redeploy", []byte(time.Now().String()), 0644)
```

The watcher will detect this and run deploy.sh automatically.

## Useful Commands

```bash
# Check status
launchctl list | grep bud

# View logs
tail -f ~/Library/Logs/bud.log

# Manual restart
launchctl kickstart -k gui/$(id -u)/com.bud.daemon

# Stop bud
launchctl stop com.bud.daemon

# Start bud
launchctl start com.bud.daemon

# Unload services (disable)
launchctl unload ~/Library/LaunchAgents/com.bud.daemon.plist
launchctl unload ~/Library/LaunchAgents/com.bud.watcher.plist
```

## Troubleshooting

**Service won't start:**
- Check logs: `tail -100 ~/Library/Logs/bud.log`
- Verify .env exists and has valid tokens
- Ensure binaries are built: `ls -la ~/bud2/bin/`

**Claude CLI not found:**
- Ensure PATH includes npm global bin: `npm bin -g`
- May need to add to plist EnvironmentVariables

**fswatch not triggering:**
- Check watcher logs: `tail ~/Library/Logs/bud-watcher.log`
- Verify trigger file exists: `ls -la /tmp/bud-redeploy`
- Test manually: `touch /tmp/bud-redeploy`
