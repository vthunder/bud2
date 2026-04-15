#!/bin/bash
# Run deadcode, filtering known false positives listed in deadcode.exclude.
# Usage: ./scripts/deadcode.sh [deadcode flags] <packages>

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXCLUDE_FILE="$SCRIPT_DIR/deadcode.exclude"

output=$(deadcode "$@" 2>&1) || true

if [[ -z "$output" ]]; then
    exit 0
fi

filtered=$(echo "$output" | while IFS= read -r line; do
    excluded=false
    if [[ -f "$EXCLUDE_FILE" ]]; then
        while IFS= read -r pattern; do
            # Skip blank lines and comments
            [[ -z "$pattern" || "$pattern" == \#* ]] && continue
            if [[ "$line" == *"$pattern"* ]]; then
                excluded=true
                break
            fi
        done < "$EXCLUDE_FILE"
    fi
    $excluded || echo "$line"
done)

if [[ -n "$filtered" ]]; then
    echo "$filtered"
    exit 1
fi
