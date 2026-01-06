#!/bin/bash
# Test memory scenarios

set -e
cd /Users/thunder/src/bud2

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

# Parse args
case "${1:-}" in
    --list|-l)
        ./bin/test-synthetic -list
        ;;
    --all|-a)
        ./bin/test-synthetic -all "${@:2}"
        ;;
    --help|-h)
        echo "Usage:"
        echo "  ./scripts/test-memory.sh                    # Run default (short-recall) scenario"
        echo "  ./scripts/test-memory.sh -l|--list          # List available scenarios"
        echo "  ./scripts/test-memory.sh -a|--all           # Run all scenarios"
        echo "  ./scripts/test-memory.sh <scenario-name>    # Run specific scenario"
        echo "  ./scripts/test-memory.sh -v                 # Verbose output"
        echo ""
        echo "Scenarios are YAML files in tests/scenarios/"
        ;;
    "")
        ./bin/test-synthetic
        ;;
    -*)
        ./bin/test-synthetic "$@"
        ;;
    *)
        # Assume it's a scenario name
        ./bin/test-synthetic -scenario "tests/scenarios/$1.yaml" "${@:2}"
        ;;
esac
