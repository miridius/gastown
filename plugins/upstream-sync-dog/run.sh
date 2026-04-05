#!/bin/bash
# upstream-sync-dog: Sync forked rigs with upstream repositories.
#
# Runs gt upstream sync for all configured rigs. On failure, files a bead
# with details for human resolution.

set -euo pipefail

echo "=== Upstream Sync Dog: Starting ==="

# Run the sync command
OUTPUT=$(gt upstream sync --skip-tests 2>&1) || {
    EXIT_CODE=$?
    echo "$OUTPUT"
    echo ""
    echo "=== Upstream sync encountered issues (exit $EXIT_CODE) ==="

    # File a bead with the failure details
    bd create "upstream-sync-dog: sync issues detected" -t bug --ephemeral \
        -l type:plugin-run,plugin:upstream-sync-dog,result:warning \
        -d "Upstream sync completed with issues. Review output:

$OUTPUT" --silent 2>/dev/null || true

    # Also run status for diagnostic info
    echo ""
    echo "=== Current upstream status ==="
    gt upstream status 2>&1 || true

    exit $EXIT_CODE
}

echo "$OUTPUT"

# Record success
bd create "upstream-sync-dog: sync complete" -t chore --ephemeral \
    -l type:plugin-run,plugin:upstream-sync-dog,result:success \
    -d "$OUTPUT" --silent 2>/dev/null || true

echo ""
echo "=== Upstream Sync Dog: Complete ==="
