#!/bin/bash
# Test memory consolidation using synthetic mode

set -e

cd /Users/thunder/src/bud2

echo "=== Memory Test Script ==="
echo ""

if [ "$1" = "--auto" ]; then
    echo "Running automated synthetic test..."
    echo ""

    # Build if needed
    if [ ! -f bin/bud ] || [ cmd/bud/main.go -nt bin/bud ]; then
        echo "Building bud..."
        go build -o bin/bud ./cmd/bud
    fi

    if [ ! -f bin/test-synthetic ] || [ cmd/test-synthetic/main.go -nt bin/test-synthetic ]; then
        echo "Building test-synthetic..."
        go build -o bin/test-synthetic ./cmd/test-synthetic
    fi

    echo ""
    ./bin/test-synthetic
    exit 0
fi

echo "Usage:"
echo "  ./scripts/test-memory.sh --auto    # Automated synthetic test"
echo "  ./scripts/test-memory.sh           # Manual test with Discord"
echo ""
echo "The automated test:"
echo "  1. Starts bud in SYNTHETIC_MODE (no Discord needed)"
echo "  2. Writes messages to inbox.jsonl"
echo "  3. Reads responses from outbox.jsonl"
echo "  4. Tests memory recall with a 'secret code word'"
echo ""
echo "For manual testing with Discord:"
echo "  1. Run: ./bin/bud"
echo "  2. Send messages in Discord"
echo "  3. Check state/traces.json for consolidated memories"
