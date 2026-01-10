#!/bin/bash
# Watch for redeploy trigger file
# This script is run by launchd and uses fswatch to monitor for changes

TRIGGER_FILE="${TRIGGER_FILE:-/tmp/bud-redeploy}"
BUD_DIR="${BUD_DIR:-$HOME/bud2}"
LOG_FILE="${BUD_LOG:-$HOME/Library/Logs/bud.log}"

echo "$(date): Watcher started, monitoring $TRIGGER_FILE" >> "$LOG_FILE"

# Create trigger file if it doesn't exist
touch "$TRIGGER_FILE"

# Watch the trigger file for modifications
fswatch -o "$TRIGGER_FILE" | while read -r; do
    echo "$(date): Redeploy triggered via $TRIGGER_FILE" >> "$LOG_FILE"
    "$BUD_DIR/deploy/deploy.sh"
done
