# Bud Deployment Setup for Mac Mini

## Prerequisites

On the Mac Mini, install:

```bash
# Homebrew (if not installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Go
brew install go

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

   # Generate daemon plist
   sed "s|/Users/YOU|$HOME|g; s|/Users/thunder/src/bud2|$HOME/bud2|g" \
       com.bud.daemon.plist.example > com.bud.daemon.plist
   ```

2. **Build:**
   ```bash
   cd ~/bud2
   mkdir -p bin
   go build -o bin/bud ./cmd/bud
   go build -o bin/bud-mcp ./cmd/bud-mcp
   ```

3. **Install service:**
   ```bash
   cp ~/bud2/deploy/com.bud.daemon.plist ~/Library/LaunchAgents/
   launchctl load ~/Library/LaunchAgents/com.bud.daemon.plist
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

Bud can request a redeploy using the MCP tool:
```
trigger_redeploy(reason="Updated code ready")
```

This runs `deploy.sh` in the background, which pulls latest code, rebuilds, and restarts the service.

Alternatively, from a bash session:
```bash
~/bud2/deploy/deploy.sh
```

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

# Unload service (disable)
launchctl unload ~/Library/LaunchAgents/com.bud.daemon.plist
```

## Troubleshooting

**Service won't start:**
- Check logs: `tail -100 ~/Library/Logs/bud.log`
- Verify .env exists and has valid tokens
- Ensure binaries are built: `ls -la ~/bud2/bin/`

**Claude CLI not found:**
- Ensure PATH includes npm global bin: `npm bin -g`
- May need to add to plist EnvironmentVariables
