#!/bin/bash
# Setup script for bud deployment
# Generates deploy scripts and launchd plists with correct paths

set -e

# Determine bud directory (default: parent of this script's directory)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUD_DIR="${BUD_DIR:-$(dirname "$SCRIPT_DIR")}"
USERNAME="$(whoami)"

echo "Setting up bud deployment..."
echo "  BUD_DIR: $BUD_DIR"
echo "  USER: $USERNAME"
echo ""

# Check prerequisites
check_prereqs() {
    local missing=()
    command -v go >/dev/null || missing+=("go (brew install go)")
    command -v fswatch >/dev/null || missing+=("fswatch (brew install fswatch)")
    command -v claude >/dev/null || missing+=("claude (npm install -g @anthropic-ai/claude-code)")

    if [ ${#missing[@]} -gt 0 ]; then
        echo "Missing prerequisites:"
        for m in "${missing[@]}"; do
            echo "  - $m"
        done
        echo ""
        read -p "Continue anyway? [y/N] " -n 1 -r
        echo ""
        [[ $REPLY =~ ^[Yy]$ ]] || exit 1
    fi
}

# Generate deploy.sh from example
generate_deploy() {
    echo "Generating deploy.sh..."
    sed "s|@BUD_DIR@|$BUD_DIR|g; s|@HOME@|$HOME|g" \
        "$SCRIPT_DIR/deploy.sh.example" > "$SCRIPT_DIR/deploy.sh"
    chmod +x "$SCRIPT_DIR/deploy.sh"
}

# Generate plist files from examples
generate_plists() {
    echo "Generating launchd plist files..."

    for plist in com.bud.daemon.plist com.bud.watcher.plist; do
        sed "s|@BUD_DIR@|$BUD_DIR|g; s|@HOME@|$HOME|g" \
            "$SCRIPT_DIR/${plist}.example" > "$SCRIPT_DIR/$plist"
    done
}

# Create bin directory and build
build_bud() {
    echo "Building bud..."
    mkdir -p "$BUD_DIR/bin"
    cd "$BUD_DIR"
    go build -o bin/bud ./cmd/bud
    go build -o bin/bud-mcp ./cmd/bud-mcp
    echo "  Built: $BUD_DIR/bin/bud"
    echo "  Built: $BUD_DIR/bin/bud-mcp"
}

# Install launchd services
install_services() {
    echo ""
    read -p "Install launchd services? [y/N] " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        mkdir -p ~/Library/LaunchAgents
        cp "$SCRIPT_DIR/com.bud.daemon.plist" ~/Library/LaunchAgents/
        cp "$SCRIPT_DIR/com.bud.watcher.plist" ~/Library/LaunchAgents/
        echo "  Copied plists to ~/Library/LaunchAgents/"

        echo ""
        read -p "Load services now? [y/N] " -n 1 -r
        echo ""
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            launchctl load ~/Library/LaunchAgents/com.bud.daemon.plist 2>/dev/null || true
            launchctl load ~/Library/LaunchAgents/com.bud.watcher.plist 2>/dev/null || true
            echo "  Services loaded"
        fi
    fi
}

# Check for .env
check_env() {
    if [ ! -f "$BUD_DIR/.env" ]; then
        echo ""
        echo "WARNING: No .env file found!"
        echo "  Copy .env.example to .env and configure it:"
        echo "  cp $BUD_DIR/.env.example $BUD_DIR/.env"
    fi
}

# Main
check_prereqs
generate_deploy
generate_plists
build_bud
check_env
install_services

echo ""
echo "Setup complete!"
echo ""
echo "To deploy from another machine:"
echo "  ssh <this-machine> \"$BUD_DIR/deploy/deploy.sh\""
echo ""
echo "To trigger redeploy from bud:"
echo "  touch /tmp/bud-redeploy"
echo ""
echo "View logs:"
echo "  tail -f ~/Library/Logs/bud.log"
