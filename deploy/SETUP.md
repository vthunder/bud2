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

## Quick Setup

```bash
# Clone the repo
git clone https://github.com/vthunder/bud2.git ~/bud2
cd ~/bud2

# Create .env with your tokens
cp .env.example .env
nano .env  # Add Discord token, channel ID, etc.

# Run setup script (builds, generates plists, optionally installs services)
./deploy/setup.sh
```

The setup script will:
1. Generate `deploy.sh` and launchd plists with correct paths
2. Build the bud binaries
3. Optionally install and load launchd services

## Manual Setup

If you prefer to set things up manually:

1. **Generate files from examples:**
   ```bash
   cd ~/bud2/deploy

   # Generate deploy.sh
   sed "s|\$HOME/src/bud2|$HOME/bud2|g" deploy.sh.example > deploy.sh
   chmod +x deploy.sh

   # Generate plists
   sed "s|/Users/YOU|$HOME|g; s|/Users/thunder/src/bud2|$HOME/bud2|g" \
       com.bud.daemon.plist.example > com.bud.daemon.plist
   sed "s|/Users/YOU|$HOME|g; s|/Users/thunder/src/bud2|$HOME/bud2|g" \
       com.bud.watcher.plist.example > com.bud.watcher.plist
   ```

2. **Build:**
   ```bash
   cd ~/bud2
   mkdir -p bin
   go build -o bin/bud ./cmd/bud
   go build -o bin/bud-mcp ./cmd/bud-mcp
   ```

3. **Install services:**
   ```bash
   cp ~/bud2/deploy/com.bud.*.plist ~/Library/LaunchAgents/
   launchctl load ~/Library/LaunchAgents/com.bud.daemon.plist
   launchctl load ~/Library/LaunchAgents/com.bud.watcher.plist
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

Or use the MCP tool:
```
trigger_redeploy(reason="Updated code ready")
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
