#!/bin/bash
# Run the spaCy NER sidecar server
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
exec "$SCRIPT_DIR/.venv/bin/python" "$SCRIPT_DIR/server.py" "$@"
