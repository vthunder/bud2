#!/bin/bash
# Wrapper script to run bud with proper logging
# launchd's stdout capture can miss output from fast-crashing processes

LOG_FILE="${HOME}/Library/Logs/bud.log"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUD_DIR="$(dirname "$SCRIPT_DIR")"

cd "$BUD_DIR"

echo "$(date): === Starting bud ===" >> "$LOG_FILE"

# Run bud, capturing both stdout and stderr to log file
exec "$BUD_DIR/bin/bud" >> "$LOG_FILE" 2>&1
