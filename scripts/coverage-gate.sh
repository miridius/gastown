#!/usr/bin/env bash
# coverage-gate.sh — Enforce minimum test coverage on fork-local changed files.
#
# Usage:
#   scripts/coverage-gate.sh [--threshold N] [--base REF]
#
# Defaults:
#   --threshold 80   (minimum % coverage per changed file)
#   --base upstream/main
#
# The script:
#   1. Generates coverage profile if not already present (coverage.out)
#   2. Finds .go files changed between --base and HEAD (excluding tests, generated, plugins)
#   3. Computes per-file coverage from the profile
#   4. Fails if any changed file is below the threshold
#
# Exit codes:
#   0 — all changed files meet threshold (or no changed files)
#   1 — one or more files below threshold
#   2 — script error (missing tools, bad args)

set -euo pipefail

THRESHOLD=80
BASE_REF="upstream/main"
COVERAGE_FILE="coverage.out"
SKIP_GENERATE=false

usage() {
    echo "Usage: $0 [--threshold N] [--base REF] [--coverage-file FILE] [--skip-generate]"
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --threshold) THRESHOLD="$2"; shift 2 ;;
        --base) BASE_REF="$2"; shift 2 ;;
        --coverage-file) COVERAGE_FILE="$2"; shift 2 ;;
        --skip-generate) SKIP_GENERATE=true; shift ;;
        -h|--help) usage ;;
        *) echo "Unknown option: $1"; usage ;;
    esac
done

# Patterns to exclude from coverage checks (untestable or out of scope)
EXCLUDE_PATTERNS=(
    "plugins/"               # Plugins are standalone — separate coverage
    "cmd/gt/main.go"         # Entry point, not unit-testable
    "cmd/gt-proxy"           # Proxy binaries
    "cmd/gt-desktop"         # Desktop binary
    "_generated.go"          # Generated code
    "internal/formula/formulas/" # Generated formula templates
    "internal/cmd/"          # Cobra command handlers — covered by integration tests
    "internal/daemon/"       # Daemon loop code — requires integration tests
    "internal/deps/"         # Version constants only
    "internal/git/"          # Git operations — requires repo fixtures
    "internal/health/"       # Health checks — requires running services
    "internal/mail/"         # Mail routing — requires Dolt
    "internal/reaper/"       # Reaper — requires Dolt
    "internal/refinery/"     # Refinery — covered by integration tests
    "internal/session/"      # Session management — OS-level
    "internal/lock/"         # File locking — OS-level
    "internal/mayor/"        # Mayor process management — OS-level
    "internal/nudge/"        # Nudge polling — OS-level
    "internal/tmux/"         # Tmux integration — requires tmux
    "internal/util/exec_"    # Exec helpers — OS-level
    "internal/util/orphan"   # Process management — OS-level
    "internal/quota/"        # Keychain stubs
    "internal/witness/"      # Witness handlers — requires integration
    "sysproc_unix.go"        # System process helpers
    "signals_unix.go"        # Signal handlers
)

should_exclude() {
    local file="$1"
    for pattern in "${EXCLUDE_PATTERNS[@]}"; do
        if [[ "$file" == *"$pattern"* ]]; then
            return 0
        fi
    done
    return 1
}

# Step 1: Generate coverage if needed
if [[ "$SKIP_GENERATE" == false ]] && [[ ! -f "$COVERAGE_FILE" ]]; then
    echo "Generating coverage profile..."
    go test -short -timeout=10m -coverprofile="$COVERAGE_FILE" ./... 2>&1 | tail -5
    echo ""
fi

if [[ ! -f "$COVERAGE_FILE" ]]; then
    echo "ERROR: Coverage file $COVERAGE_FILE not found"
    echo "Run 'go test -coverprofile=$COVERAGE_FILE ./...' first, or omit --skip-generate"
    exit 2
fi

# Step 2: Get changed .go files (source only, not tests)
echo "Comparing against $BASE_REF..."
if ! git rev-parse "$BASE_REF" >/dev/null 2>&1; then
    echo "WARNING: $BASE_REF not found. Trying to fetch..."
    git fetch upstream main 2>/dev/null || true
    if ! git rev-parse "$BASE_REF" >/dev/null 2>&1; then
        echo "ERROR: Cannot resolve $BASE_REF. Ensure upstream remote is configured."
        exit 2
    fi
fi

mapfile -t CHANGED_FILES < <(
    git diff --name-only --diff-filter=ACMR "$BASE_REF"...HEAD -- '*.go' |
    grep -v '_test.go$' |
    grep -v '/testdata/' |
    grep -v '/testutil/' |
    while read -r f; do [ -f "$f" ] && echo "$f"; done |
    sort
)

if [[ ${#CHANGED_FILES[@]} -eq 0 ]]; then
    echo "No changed Go source files found. Nothing to check."
    exit 0
fi

echo "Found ${#CHANGED_FILES[@]} changed Go source files"
echo "Threshold: ${THRESHOLD}%"
echo ""

# Step 3: Parse coverage per file from go tool cover output
# go tool cover -func outputs: file:line: funcName coverage%
# We aggregate to per-file by averaging statement coverage
declare -A FILE_COVERAGE
declare -A FILE_SEEN

while IFS= read -r line; do
    # Skip the total line
    if [[ "$line" == total:* ]]; then continue; fi
    # Parse: github.com/org/repo/path/file.go:42:  FuncName  85.7%
    file_part="${line%%:*}"
    coverage_part="${line##*$'\t'}"
    coverage_part="${coverage_part%%%*}"
    coverage_part="$(echo "$coverage_part" | tr -d ' ')"

    # Convert module path to relative path
    # The coverage output uses the full module path
    # Strip the module prefix to get relative path
    rel_path="${file_part#github.com/steveyegge/gastown/}"
    # Also try miridius fork path
    rel_path="${rel_path#github.com/miridius/gastown/}"

    if [[ -n "${FILE_SEEN[$rel_path]+x}" ]]; then
        # Accumulate: we'll compute weighted average later
        # For simplicity, track sum and count
        old_sum="${FILE_COVERAGE[$rel_path]}"
        old_count="${FILE_SEEN[$rel_path]}"
        FILE_COVERAGE[$rel_path]="$(echo "$old_sum + $coverage_part" | bc)"
        FILE_SEEN[$rel_path]=$((old_count + 1))
    else
        FILE_COVERAGE[$rel_path]="$coverage_part"
        FILE_SEEN[$rel_path]=1
    fi
done < <(go tool cover -func="$COVERAGE_FILE" 2>/dev/null)

# Step 4: Check each changed file
PASS=0
FAIL=0
SKIP=0
NOCOV=0
FAILURES=()

for file in "${CHANGED_FILES[@]}"; do
    if should_exclude "$file"; then
        SKIP=$((SKIP + 1))
        continue
    fi

    if [[ -z "${FILE_SEEN[$file]+x}" ]]; then
        # File has no coverage data — either not in any test package or 0%
        NOCOV=$((NOCOV + 1))
        FAILURES+=("  $file: NO COVERAGE DATA (0%)")
        FAIL=$((FAIL + 1))
        continue
    fi

    sum="${FILE_COVERAGE[$file]}"
    count="${FILE_SEEN[$file]}"
    avg="$(echo "scale=1; $sum / $count" | bc)"
    avg_int="$(echo "$avg" | cut -d. -f1)"
    # Handle empty int (when avg is like .5)
    [[ -z "$avg_int" ]] && avg_int=0

    if [[ "$avg_int" -ge "$THRESHOLD" ]]; then
        PASS=$((PASS + 1))
        echo "  ✓ $file: ${avg}%"
    else
        FAIL=$((FAIL + 1))
        FAILURES+=("  ✗ $file: ${avg}% (need ${THRESHOLD}%)")
        echo "  ✗ $file: ${avg}% < ${THRESHOLD}%"
    fi
done

echo ""
echo "Coverage Gate Results"
echo "====================="
echo "  Passed:     $PASS"
echo "  Failed:     $FAIL"
echo "  Skipped:    $SKIP (excluded patterns)"
echo "  No data:    $NOCOV"
echo ""

if [[ $FAIL -gt 0 ]]; then
    echo "FAILED — ${FAIL} file(s) below ${THRESHOLD}% coverage:"
    for f in "${FAILURES[@]}"; do
        echo "$f"
    done
    echo ""
    echo "To fix: add tests for the listed files, or adjust exclusions in this script."
    exit 1
else
    echo "PASSED — all ${PASS} changed files meet ${THRESHOLD}% coverage threshold"
    exit 0
fi
