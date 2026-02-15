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
    command -v python3.12 >/dev/null || missing+=("python@3.12 (brew install python@3.12) - for NER sidecar")

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

    for plist in com.bud.daemon.plist com.bud.watcher.plist com.bud.ner-sidecar.plist; do
        if [ -f "$SCRIPT_DIR/${plist}.example" ]; then
            sed "s|@BUD_DIR@|$BUD_DIR|g; s|@HOME@|$HOME|g" \
                "$SCRIPT_DIR/${plist}.example" > "$SCRIPT_DIR/$plist"
        fi
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
        for plist in com.bud.daemon.plist com.bud.watcher.plist com.bud.ner-sidecar.plist; do
            if [ -f "$SCRIPT_DIR/$plist" ]; then
                cp "$SCRIPT_DIR/$plist" ~/Library/LaunchAgents/
            fi
        done
        echo "  Copied plists to ~/Library/LaunchAgents/"

        echo ""
        read -p "Load services now? [y/N] " -n 1 -r
        echo ""
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            launchctl load ~/Library/LaunchAgents/com.bud.daemon.plist 2>/dev/null || true
            launchctl load ~/Library/LaunchAgents/com.bud.watcher.plist 2>/dev/null || true
            launchctl load ~/Library/LaunchAgents/com.bud.ner-sidecar.plist 2>/dev/null || true
            echo "  Services loaded"
        fi
    fi
}

# Setup NER sidecar Python environment
setup_sidecar() {
    echo "Setting up NER sidecar..."
    SIDECAR_DIR="$BUD_DIR/sidecar"
    VENV_DIR="$SIDECAR_DIR/.venv"

    if [ ! -d "$VENV_DIR" ]; then
        PYTHON_BIN="$(command -v python3.12 || command -v python3)"
        echo "  Creating venv with $PYTHON_BIN..."
        "$PYTHON_BIN" -m venv "$VENV_DIR"
        echo "  Installing dependencies..."
        "$VENV_DIR/bin/pip" install -q spacy fastapi uvicorn
        "$VENV_DIR/bin/python" -m spacy download en_core_web_sm
    else
        echo "  Sidecar venv already exists"
    fi
}

# Setup Things integration (optional)
setup_things() {
    echo ""
    read -p "Setup Things 3 integration? [y/N] " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        THINGS_DIR="$BUD_DIR/things-mcp"
        if [ ! -d "$THINGS_DIR" ]; then
            echo "  Things MCP directory not found at $THINGS_DIR"
            return
        fi

        echo "Building things-mcp server..."
        cd "$THINGS_DIR"
        if [ ! -d "node_modules" ]; then
            echo "  Installing npm dependencies..."
            npm install
        fi
        npm run build
        echo "  Things MCP server built successfully"

        echo ""
        echo "Next, you need to grant automation permissions."
        read -p "Grant permissions now? [y/N] " -n 1 -r
        echo ""
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            "$SCRIPT_DIR/grant-things-permissions.sh"
        else
            echo "  You can grant permissions later by running:"
            echo "  $SCRIPT_DIR/grant-things-permissions.sh"
        fi

        cd "$BUD_DIR"
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
setup_sidecar
setup_things
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
echo "  tail -f ~/Library/Logs/ner-sidecar.log"
