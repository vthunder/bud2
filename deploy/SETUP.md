# Bud Deployment Setup for Mac Mini

## Prerequisites

On the Mac Mini, install:

```bash
# Homebrew (if not installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Go
brew install go

# Python 3.12 (for NER sidecar)
brew install python@3.12

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
3. Set up the NER sidecar Python venv (spaCy + FastAPI)
4. Optionally install and load launchd services (including NER sidecar)

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
tail -f ~/Library/Logs/ner-sidecar.log

# Manual restart
launchctl kickstart -k gui/$(id -u)/com.bud.daemon

# Restart NER sidecar
launchctl kickstart -k gui/$(id -u)/com.bud.ner-sidecar

# Stop bud
launchctl stop com.bud.daemon

# Start bud
launchctl start com.bud.daemon

# Unload service (disable)
launchctl unload ~/Library/LaunchAgents/com.bud.daemon.plist
```

## Things 3 Integration (Optional)

If you want to use the Things 3 task manager integration:

1. **Build the things-mcp server:**
   ```bash
   cd ~/bud2/things-mcp
   npm install
   npm run build
   ```

2. **Grant automation permissions:**
   ```bash
   ~/bud2/deploy/grant-things-permissions.sh
   ```

   This will trigger macOS permission dialogs. Click **Allow** when prompted to let the system control:
   - Things3
   - System Events

3. **Verify permissions:**
   Open **System Settings → Privacy & Security → Automation** and ensure the process running bud (usually shown as "launchd") has checkmarks for:
   - Things3
   - System Events

4. **The integration is configured in `.mcp.json`:**
   The `state/.mcp.json` file should include the things-mcp server configuration.

**Note:** Permissions need to be granted before the launchd service can successfully use Things integration. If bud hangs on startup, it's likely waiting for these permission dialogs.

## Troubleshooting

**Service won't start:**
- Check logs: `tail -100 ~/Library/Logs/bud.log`
- Verify .env exists and has valid tokens
- Ensure binaries are built: `ls -la ~/bud2/bin/`

**Service hangs on startup with Things integration:**
- You likely have pending permission dialogs. Run `grant-things-permissions.sh` to trigger them.
- Check System Settings → Privacy & Security → Automation for pending requests
- The things-mcp server now starts non-blocking, but initial permissions still need to be granted

**Claude CLI not found:**
- Ensure PATH includes npm global bin: `npm bin -g`
- May need to add to plist EnvironmentVariables
