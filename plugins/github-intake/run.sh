#!/usr/bin/env bash
# github-intake/run.sh — Poll GitHub issues, sanitize content, triage, and
# convert to Gas Town work items.
#
# Security model: All issue content passes through sanitization BEFORE any
# agent reads it. External users can craft issue bodies to manipulate agents —
# the sanitization step is the security boundary.
#
# Requires: `gh` CLI installed and authenticated (`gh auth status`).
#
# Usage: ./run.sh

set -euo pipefail

# --- Derive GT_RIG_ROOT if not set -------------------------------------------
# Deacon dogs may not inject GT_RIG_ROOT. Derive it from the script's location:
# plugins live at <rig_root>/plugins/<name>/run.sh, so rig root is two levels up.
if [ -z "${GT_RIG_ROOT:-}" ]; then
  _SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  GT_RIG_ROOT="$(cd "$_SCRIPT_DIR/../.." && pwd)"
  if [ ! -d "$GT_RIG_ROOT/.git" ] && ! git -C "$GT_RIG_ROOT" rev-parse --git-dir >/dev/null 2>&1; then
    echo "SKIP: GT_RIG_ROOT not set and could not derive from script path"
    exit 0
  fi
fi

# --- Step 1: Verify Prerequisites -------------------------------------------

gh auth status 2>/dev/null
if [ $? -ne 0 ]; then
  echo "SKIP: gh CLI not authenticated"
  exit 0
fi

REPO=$(git -C "$GT_RIG_ROOT" remote get-url origin 2>/dev/null \
  | sed -E 's|.*github\.com[:/]||; s|\.git$||')

if [ -z "$REPO" ]; then
  echo "SKIP: could not detect GitHub repo from rig remote"
  exit 0
fi

REPO_OWNER=$(echo "$REPO" | cut -d'/' -f1)
echo "Monitoring repo: $REPO (owner: $REPO_OWNER)"

# --- Step 2: Fetch New GitHub Issues -----------------------------------------

# Fetch open issues created in the last 7 days
ISSUES=$(gh issue list --repo "$REPO" --state open \
  --json number,title,body,author,labels,createdAt,url \
  --limit 50 2>/dev/null)

ISSUE_COUNT=$(echo "$ISSUES" | jq 'length')
if [ "$ISSUE_COUNT" -eq 0 ]; then
  echo "No open issues found for $REPO"
  exit 0
fi

echo "Found $ISSUE_COUNT open issue(s)"

# --- Step 3: Identify Already-Processed Issues -------------------------------

# Get rig name for routing
RIG_NAME=$(basename "$(dirname "$(dirname "$GT_RIG_ROOT")")" 2>/dev/null)
RIG_FLAG=""
[ -n "$RIG_NAME" ] && RIG_FLAG="--rig $RIG_NAME"

# List existing intake beads to avoid duplicates
EXISTING=$(bd list --label github-intake --json $RIG_FLAG 2>/dev/null || echo "[]")

# --- Step 4: Process Each Issue ----------------------------------------------

# CRITICAL: Sanitization happens HERE, before any content enters agent context.
#
# Sanitization rules (applied to both title and body):
#   1. Strip system prompt overrides: <system>, <system_prompt>, <instructions>,
#      <system-reminder> tags and variants
#   2. Strip role manipulation: "ignore previous instructions", "you are now",
#      "forget all context", "new instructions:", etc.
#   3. Strip command injection: "run the following command", <tool_use>,
#      <function_call>, < tags
#   4. Strip data exfiltration: "send the X to", "email the X to", curl with -d
#   5. Strip hidden content: zero-width characters, HTML comments containing instructions
#   6. Truncate: Titles > 200 chars, bodies > 10000 chars
#
# After sanitization, flag issues with detected injection attempts for manual review.

CREATED=0
SKIPPED=0
FLAGGED=0

while IFS= read -r ISSUE_JSON; do
  [ -z "$ISSUE_JSON" ] && continue

  GH_NUM=$(echo "$ISSUE_JSON" | jq -r '.number')
  GH_TITLE=$(echo "$ISSUE_JSON" | jq -r '.title')
  GH_BODY=$(echo "$ISSUE_JSON" | jq -r '.body // ""')
  GH_AUTHOR=$(echo "$ISSUE_JSON" | jq -r '.author.login')
  GH_URL=$(echo "$ISSUE_JSON" | jq -r '.url')
  GH_LABELS=$(echo "$ISSUE_JSON" | jq -r '[.labels[].name] | join(",")')

  # Check if already processed (match on GitHub issue number in description)
  if echo "$EXISTING" | jq -e --arg num "GH#$GH_NUM" \
    '.[] | select(.title | contains($num))' > /dev/null 2>&1; then
    SKIPPED=$((SKIPPED + 1))
    continue
  fi

  # === SANITIZATION BOUNDARY ===
  # Use the Go sanitize package via gt sanitize command if available,
  # otherwise apply shell-based sanitization as fallback.

  SANITIZED_TITLE="$GH_TITLE"
  SANITIZED_BODY="$GH_BODY"
  INJECTION_FLAGS=""

  if command -v gt >/dev/null 2>&1 && gt sanitize --help >/dev/null 2>&1; then
    # Prefer Go-based sanitization (more thorough)
    SANITIZE_RESULT=$(echo "$GH_TITLE" | gt sanitize --json 2>/dev/null)
    if [ -n "$SANITIZE_RESULT" ]; then
      SANITIZED_TITLE=$(echo "$SANITIZE_RESULT" | jq -r '.content')
      TITLE_FLAGS=$(echo "$SANITIZE_RESULT" | jq -r '.flags | join(",")')
    fi

    SANITIZE_RESULT=$(echo "$GH_BODY" | gt sanitize --json 2>/dev/null)
    if [ -n "$SANITIZE_RESULT" ]; then
      SANITIZED_BODY=$(echo "$SANITIZE_RESULT" | jq -r '.content')
      BODY_FLAGS=$(echo "$SANITIZE_RESULT" | jq -r '.flags | join(",")')
    fi

    [ -n "$TITLE_FLAGS" ] && INJECTION_FLAGS="$TITLE_FLAGS"
    [ -n "$BODY_FLAGS" ] && INJECTION_FLAGS="${INJECTION_FLAGS:+$INJECTION_FLAGS,}$BODY_FLAGS"
  else
    # Shell-based fallback sanitization
    # Strip system/instruction XML-like tags
    SANITIZED_TITLE=$(echo "$SANITIZED_TITLE" | sed -E 's/<\s*\/?\s*(system|instructions?|system[-_]?prompt|system[-_]?reminder)\s*>//gi')
    SANITIZED_BODY=$(echo "$SANITIZED_BODY" | sed -E 's/<\s*\/?\s*(system|instructions?|system[-_]?prompt|system[-_]?reminder)\s*>//gi')

    # Strip zero-width characters
    SANITIZED_BODY=$(echo "$SANITIZED_BODY" | tr -d '\u200B\u200C\u200D\uFEFF\u2060')

    # Strip HTML comments
    SANITIZED_BODY=$(echo "$SANITIZED_BODY" | sed 's/<!--.*-->//g')

    # Truncate body
    SANITIZED_BODY=$(echo "$SANITIZED_BODY" | head -c 10000)

    # Detect injection (simple check)
    if echo "$GH_TITLE$GH_BODY" | grep -qiE '(ignore\s+(all\s+)?previous|<system|you\s+are\s+now|new\s+instructions:)'; then
      INJECTION_FLAGS="possible-injection"
    fi
  fi

  # Truncate title if too long
  if [ ${#SANITIZED_TITLE} -gt 200 ]; then
    SANITIZED_TITLE="${SANITIZED_TITLE:0:200}..."
  fi

  # === ROUTING: Auto-approve or flag for review ===
  AUTO_APPROVE=false

  # Check if author is the repo owner
  if [ "$GH_AUTHOR" = "$REPO_OWNER" ]; then
    AUTO_APPROVE=true
  fi

  # Check if author is a collaborator (has push access)
  if [ "$AUTO_APPROVE" = false ]; then
    PERM=$(gh api "repos/$REPO/collaborators/$GH_AUTHOR/permission" \
      --jq '.permission' 2>/dev/null || echo "none")
    case "$PERM" in
      admin|maintain|write) AUTO_APPROVE=true ;;
    esac
  fi

  # If injection was detected, NEVER auto-approve
  if [ -n "$INJECTION_FLAGS" ]; then
    AUTO_APPROVE=false
  fi

  # === CREATE BEAD OR FLAG FOR REVIEW ===

  BEAD_TITLE="GH#$GH_NUM: $SANITIZED_TITLE"

  # Build description with sanitized content and metadata
  DESCRIPTION="github_issue: $GH_NUM
github_repo: $REPO
github_author: $GH_AUTHOR
github_url: $GH_URL
auto_approved: $AUTO_APPROVE"

  if [ -n "$INJECTION_FLAGS" ]; then
    DESCRIPTION="$DESCRIPTION
sanitization_flags: $INJECTION_FLAGS"
  fi

  DESCRIPTION="$DESCRIPTION

---

$SANITIZED_BODY"

  if [ "$AUTO_APPROVE" = true ]; then
    # Auto-approved: create a task bead ready for work
    BEAD_ID=$(bd create "$BEAD_TITLE" -t task -p 2 \
      -d "$DESCRIPTION" \
      -l github-intake,auto-approved \
      $RIG_FLAG \
      --json 2>/dev/null | jq -r '.id // empty')

    if [ -n "$BEAD_ID" ]; then
      CREATED=$((CREATED + 1))

      # Comment on GitHub issue with tracking reference
      gh issue comment "$GH_NUM" --repo "$REPO" \
        --body "Accepted — tracking as \`$BEAD_ID\` in Gas Town." \
        2>/dev/null || true

      gt activity emit github_intake_accepted \
        --message "GitHub issue #$GH_NUM ($REPO) accepted as $BEAD_ID" \
        2>/dev/null || true
    fi
  else
    # Needs review: create bead flagged for approval
    LABELS="github-intake,needs-review"
    [ -n "$INJECTION_FLAGS" ] && LABELS="$LABELS,injection-flagged"

    BEAD_ID=$(bd create "$BEAD_TITLE" -t task -p 3 \
      -d "$DESCRIPTION" \
      -l "$LABELS" \
      $RIG_FLAG \
      --json 2>/dev/null | jq -r '.id // empty')

    if [ -n "$BEAD_ID" ]; then
      FLAGGED=$((FLAGGED + 1))

      # Notify crew/mayor for review decision
      gt mail send mayor/ -s "GitHub intake: review needed for #$GH_NUM" \
        -m "Issue #$GH_NUM from $GH_AUTHOR needs approval. Bead: $BEAD_ID" \
        2>/dev/null || true

      gt activity emit github_intake_flagged \
        --message "GitHub issue #$GH_NUM ($REPO) flagged for review as $BEAD_ID" \
        2>/dev/null || true
    fi
  fi
done < <(echo "$ISSUES" | jq -c '.[]')

# --- Step 5: Handle Approved and Rejected Review Items -----------------------

# Check for previously flagged items that have been approved or rejected
REVIEW_ITEMS=$(bd list --label needs-review,github-intake --status closed $RIG_FLAG --json 2>/dev/null || echo "[]")

while IFS= read -r ITEM; do
  [ -z "$ITEM" ] && continue

  ITEM_ID=$(echo "$ITEM" | jq -r '.id')
  ITEM_DESC=$(echo "$ITEM" | jq -r '.description // ""')
  GH_NUM=$(echo "$ITEM_DESC" | grep '^github_issue:' | awk '{print $2}')
  CLOSE_REASON=$(echo "$ITEM" | jq -r '.close_reason // ""')

  [ -z "$GH_NUM" ] && continue

  # If closed with rejection reason, close the GitHub issue
  if echo "$CLOSE_REASON" | grep -qi 'reject'; then
    gh issue close "$GH_NUM" --repo "$REPO" \
      --comment "Closed — not actionable for this project." \
      2>/dev/null || true
  fi
done < <(echo "$REVIEW_ITEMS" | jq -c '.[]')

# --- Record Result -----------------------------------------------------------

SUMMARY="$REPO: $ISSUE_COUNT issues scanned — $CREATED accepted, $FLAGGED flagged for review, $SKIPPED already tracked"
echo "$SUMMARY"

bd create "github-intake: $SUMMARY" -t chore --ephemeral \
  -l type:plugin-run,plugin:github-intake,result:success \
  -d "$SUMMARY" --silent 2>/dev/null || true
