#!/bin/bash
# Script to grant AppleScript automation permissions for Things integration
# This needs to be run to allow bud (via launchd) to control Things 3

set -e

echo "Things Integration - Permission Setup"
echo "====================================="
echo ""
echo "For bud to integrate with Things 3, it needs AppleScript automation permissions."
echo "When running as a launchd service, these permissions need to be granted manually."
echo ""
echo "You'll see permission dialogs asking to allow automation of:"
echo "  • Things3"
echo "  • System Events"
echo ""
echo "Please click 'Allow' when prompted."
echo ""
echo "If you've already granted permissions, you can skip this."
echo ""
read -p "Press Enter to test Things integration (or Ctrl+C to skip)..."

# Test the things-mcp server by running a simple command
BUD_DIR="$(cd "$(dirname "$0")/.." && pwd)"
THINGS_MCP="$BUD_DIR/things-mcp/dist/index.js"

if [ ! -f "$THINGS_MCP" ]; then
    echo ""
    echo "Error: things-mcp server not built yet!"
    echo "Run: cd $BUD_DIR/things-mcp && npm install && npm run build"
    exit 1
fi

echo ""
echo "Testing Things integration..."
echo "This will trigger permission dialogs if needed."
echo ""

# Run a simple test that will trigger the permission dialogs
# We just need to start the server briefly to trigger the permissions
timeout 5 node "$THINGS_MCP" <<EOF 2>&1 | head -20 || true
EOF

echo ""
echo "If you saw permission dialogs and clicked 'Allow', the integration should work."
echo ""
echo "You can verify permissions in:"
echo "  System Settings → Privacy & Security → Automation"
echo ""
echo "Look for the entry that's running bud (likely 'launchd' or your terminal)"
echo "and ensure it has permission to control:"
echo "  • Things3"
echo "  • System Events"
echo ""
