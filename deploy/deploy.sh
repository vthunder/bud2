#!/bin/bash
# Deploy script for bud on Mac Mini
# Usage: ./deploy.sh [--no-restart]

set -e

BUD_DIR="${BUD_DIR:-$HOME/bud2}"
LOG_FILE="${BUD_LOG:-$HOME/Library/Logs/bud.log}"

cd "$BUD_DIR"

echo "$(date): Starting deploy..." >> "$LOG_FILE"

# Pull latest code
echo "Pulling latest code..."
git pull origin main >> "$LOG_FILE" 2>&1

# Build
echo "Building bud..."
go build -o bin/bud ./cmd/bud >> "$LOG_FILE" 2>&1
go build -o bin/bud-mcp ./cmd/bud-mcp >> "$LOG_FILE" 2>&1

echo "$(date): Build complete" >> "$LOG_FILE"

# Restart unless --no-restart flag
if [[ "$1" != "--no-restart" ]]; then
    echo "Restarting bud service..."
    launchctl kickstart -k gui/$(id -u)/com.bud.daemon 2>/dev/null || \
        launchctl stop com.bud.daemon 2>/dev/null || true
    sleep 1
    launchctl start com.bud.daemon 2>/dev/null || true
    echo "$(date): Service restarted" >> "$LOG_FILE"
fi

echo "Deploy complete!"
