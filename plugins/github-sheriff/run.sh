#!/usr/bin/env bash
# github-sheriff/run.sh — Monitor GitHub CI checks on open PRs and create beads for failures.
#
# Polls GitHub for open pull requests, categorizes them by readiness, and
# creates ci-failure beads for new failures. Implements the PR Sheriff pattern.
#
# Requires: gh CLI installed and authenticated (gh auth status).
# Exits cleanly (exit 0) if gh is not available or not authenticated.
#
# Usage: ./run.sh [--repo OWNER/REPO] [--since DAYS] [--limit N]

set -euo pipefail

# --- Configuration -----------------------------------------------------------

SINCE_DAYS="${SINCE_DAYS:-7}"
PR_LIMIT="${PR_LIMIT:-100}"
REPO_OVERRIDE=""

# --- Argument parsing ---------------------------------------------------------

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)    REPO_OVERRIDE="$2"; shift 2 ;;
    --since)   SINCE_DAYS="$2"; shift 2 ;;
    --limit)   PR_LIMIT="$2"; shift 2 ;;
    --help|-h)
      echo "Usage: $0 [--repo OWNER/REPO] [--since DAYS] [--limit N]"
      echo "  --repo OWNER/REPO   GitHub repo (default: detect from rig remote)"
      echo "  --since DAYS        Only PRs updated within N days (default: 7)"
      echo "  --limit N           Max PRs to fetch (default: 100)"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# --- Helpers ------------------------------------------------------------------

log() {
  echo "[github-sheriff] $*"
}

# --- Step 1: Verify gh CLI auth -----------------------------------------------

if ! command -v gh &>/dev/null; then
  log "SKIP: gh CLI not installed"
  exit 0
fi

if ! gh auth status &>/dev/null; then
  log "SKIP: gh CLI not authenticated"
  exit 0
fi

# --- Step 2: Detect repo ------------------------------------------------------

if [[ -n "$REPO_OVERRIDE" ]]; then
  REPO="$REPO_OVERRIDE"
else
  REPO=$(git -C "${GT_RIG_ROOT:-.}" remote get-url origin 2>/dev/null \
    | sed -E 's|.*github\.com[:/]||; s|\.git$||') || true

  if [[ -z "$REPO" ]]; then
    log "SKIP: could not detect GitHub repo from rig remote"
    exit 0
  fi
fi

log "Checking PRs for $REPO"

# --- Step 3: Fetch open PRs ---------------------------------------------------

# Compute the since date (GNU date vs BSD date)
SINCE=$(date -d "$SINCE_DAYS days ago" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
  || date -v-"${SINCE_DAYS}"d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
  || "")

PRS=$(gh pr list --repo "$REPO" --state open \
  --json number,title,author,additions,deletions,mergeable,statusCheckRollup,url,updatedAt \
  --limit "$PR_LIMIT" 2>/dev/null) || {
  ERROR="gh pr list failed for $REPO"
  log "ERROR: $ERROR"
  bd create "github-sheriff: FAILED" -t chore --ephemeral \
    -l type:plugin-run,plugin:github-sheriff,result:failure \
    -d "GitHub sheriff failed: $ERROR" --silent 2>/dev/null || true
  gt escalate "Plugin FAILED: github-sheriff" \
    --severity low \
    --reason "$ERROR" 2>/dev/null || true
  exit 1
}

# Filter to PRs updated within the since window (if SINCE was computed)
if [[ -n "$SINCE" ]]; then
  PRS=$(echo "$PRS" | jq --arg since "$SINCE" '[.[] | select(.updatedAt >= $since)]')
fi

PR_COUNT=$(echo "$PRS" | jq length)
if [[ "$PR_COUNT" -eq 0 ]]; then
  log "No open PRs found for $REPO"
  exit 0
fi

log "Found $PR_COUNT open PR(s)"

# --- Step 4: Categorize each PR -----------------------------------------------

EASY_WINS=()
NEEDS_REVIEW=()
FAILURES=()

while IFS= read -r PR_JSON; do
  [[ -z "$PR_JSON" ]] && continue

  PR_NUM=$(echo "$PR_JSON" | jq -r '.number')
  PR_TITLE=$(echo "$PR_JSON" | jq -r '.title')
  AUTHOR=$(echo "$PR_JSON" | jq -r '.author.login')
  ADDITIONS=$(echo "$PR_JSON" | jq -r '.additions // 0')
  DELETIONS=$(echo "$PR_JSON" | jq -r '.deletions // 0')
  MERGEABLE=$(echo "$PR_JSON" | jq -r '.mergeable')
  TOTAL_CHANGES=$((ADDITIONS + DELETIONS))

  # Determine CI status from statusCheckRollup
  TOTAL_CHECKS=$(echo "$PR_JSON" | jq '.statusCheckRollup | length')
  PASSING_CHECKS=$(echo "$PR_JSON" | jq '[.statusCheckRollup[] | select(
    .conclusion == "SUCCESS" or .conclusion == "NEUTRAL" or
    .conclusion == "SKIPPED" or .state == "SUCCESS"
  )] | length')

  if [[ "$TOTAL_CHECKS" -gt 0 ]] && [[ "$TOTAL_CHECKS" -eq "$PASSING_CHECKS" ]]; then
    CI_PASS=true
  else
    CI_PASS=false
  fi

  # Collect individual check failures
  while IFS= read -r CHECK; do
    [[ -z "$CHECK" ]] && continue
    CHECK_NAME=$(echo "$CHECK" | jq -r '.name')
    CHECK_URL=$(echo "$CHECK" | jq -r '.detailsUrl // .targetUrl // empty')
    FAILURES+=("$PR_NUM|$PR_TITLE|$CHECK_NAME|$CHECK_URL")
  done < <(echo "$PR_JSON" | jq -c '.statusCheckRollup[] | select(
    .conclusion == "FAILURE" or .conclusion == "CANCELLED" or
    .conclusion == "TIMED_OUT" or .state == "FAILURE" or .state == "ERROR"
  )')

  # Categorize PR
  if [[ "$MERGEABLE" == "MERGEABLE" ]] && [[ "$CI_PASS" == true ]] && [[ "$TOTAL_CHANGES" -lt 200 ]]; then
    EASY_WINS+=("PR #$PR_NUM: $PR_TITLE (by $AUTHOR, +$ADDITIONS/-$DELETIONS)")
  else
    REASONS=""
    [[ "$MERGEABLE" != "MERGEABLE" ]] && REASONS+="conflicts "
    [[ "$CI_PASS" != true ]] && REASONS+="ci-failing "
    [[ "$TOTAL_CHANGES" -ge 200 ]] && REASONS+="large(${TOTAL_CHANGES}loc) "
    NEEDS_REVIEW+=("PR #$PR_NUM: $PR_TITLE (by $AUTHOR, ${REASONS% })")
  fi
done < <(echo "$PRS" | jq -c '.[]')

# --- Step 5: Report results ---------------------------------------------------

log ""
if [[ ${#EASY_WINS[@]} -gt 0 ]]; then
  log "Easy wins (${#EASY_WINS[@]}):"
  for win in "${EASY_WINS[@]}"; do
    log "  $win"
  done
fi

if [[ ${#NEEDS_REVIEW[@]} -gt 0 ]]; then
  log "Needs review (${#NEEDS_REVIEW[@]}):"
  for item in "${NEEDS_REVIEW[@]}"; do
    log "  $item"
  done
fi

if [[ ${#FAILURES[@]} -gt 0 ]]; then
  log "CI failures (${#FAILURES[@]}):"
  for fail in "${FAILURES[@]}"; do
    log "  $fail"
  done
fi

# --- Step 6: Record result ----------------------------------------------------

SUMMARY="$REPO: $PR_COUNT PRs — ${#EASY_WINS[@]} easy win(s), ${#NEEDS_REVIEW[@]} need review, ${#FAILURES[@]} CI failure(s) detected"
log ""
log "$SUMMARY"

bd create "github-sheriff: $SUMMARY" -t chore --ephemeral \
  -l type:plugin-run,plugin:github-sheriff,result:success \
  -d "$SUMMARY" --silent 2>/dev/null || true

log "Done."
