#!/usr/bin/env bash
# claude-login-dog/run.sh — Detect Claude OAuth expiry in tmux panes and alert mayor.
#
# Scans all active tmux sessions for Claude Code login/re-auth prompts.
# When OAuth tokens expire overnight, headless agent sessions stall silently.
# This dog detects the stall and escalates for human re-authentication.
#
# No LLM calls — shell-only pattern matching.
#
# Usage: ./run.sh

set -euo pipefail

# --- Step 1: Enumerate tmux sessions -----------------------------------------

echo "=== Claude Login Dog: Scanning for OAuth expiry ==="

SESSIONS=$(tmux list-sessions -F '#{session_name}' 2>/dev/null || true)
if [ -z "$SESSIONS" ]; then
  echo "No tmux sessions found — nothing to check"
  exit 0
fi

SESSION_COUNT=$(echo "$SESSIONS" | wc -l | tr -d ' ')
echo "Found $SESSION_COUNT tmux sessions"

# --- Step 2: Scan each session for login prompts -----------------------------

# Login/auth prompt patterns (extended regex, case-insensitive).
# Each pattern targets actual Claude Code login output, not code discussion.
LOGIN_PATTERNS=(
  'console\.anthropic\.com'
  'claude\.ai/login'
  'How would you like to authenticate'
  'Authentication expired'
  'OAuth token.*(expired|revoked|invalid)'
  'open this URL.*(sign in|log in|authenticat)'
  '(sign|log) in.*(open|visit|navigate).*URL'
  'To sign in, please'
  'Please (sign|log) in'
  'session has expired.*authenticat'
)

# Build combined regex
COMBINED_PATTERN=$(IFS='|'; echo "${LOGIN_PATTERNS[*]}")

AFFECTED=()

while IFS= read -r SESSION; do
  [ -z "$SESSION" ] && continue

  # Capture last 15 lines from the active pane
  PANE_OUTPUT=$(tmux capture-pane -t "$SESSION" -p -S -15 2>/dev/null || true)
  [ -z "$PANE_OUTPUT" ] && continue

  # Check for login patterns (case-insensitive)
  if echo "$PANE_OUTPUT" | grep -qiE "$COMBINED_PATTERN"; then
    # Extract the matching line(s) for the alert
    MATCH=$(echo "$PANE_OUTPUT" | grep -iE "$COMBINED_PATTERN" | head -3)
    AFFECTED+=("$SESSION")
    echo "  DETECTED: $SESSION"
    echo "    Match: $MATCH"
  fi
done <<< "$SESSIONS"

echo ""
echo "Scan complete: ${#AFFECTED[@]} sessions with login prompts detected"

# --- Step 3: Alert on detection -----------------------------------------------

if [ ${#AFFECTED[@]} -eq 0 ]; then
  echo "=== All clear — no OAuth expiry detected ==="

  bd create "claude-login-dog: all clear" -t chore --ephemeral \
    -l "type:plugin-run,plugin:claude-login-dog,result:success" \
    -d "Scanned $SESSION_COUNT sessions — no OAuth expiry detected" \
    --silent 2>/dev/null || true

  exit 0
fi

# Build the affected sessions list for the escalation message
AFFECTED_LIST=""
for SESSION in "${AFFECTED[@]}"; do
  PANE_OUTPUT=$(tmux capture-pane -t "$SESSION" -p -S -5 2>/dev/null || true)
  SNIPPET=$(echo "$PANE_OUTPUT" | grep -iE "$COMBINED_PATTERN" | head -1)
  AFFECTED_LIST="${AFFECTED_LIST}  - ${SESSION}: ${SNIPPET}
"
done

echo "=== OAuth expiry detected — escalating to mayor ==="

gt escalate "Claude OAuth expired: ${#AFFECTED[@]} session(s) need re-authentication" \
  -s HIGH \
  --reason "claude-login-dog detected OAuth login prompts in ${#AFFECTED[@]} tmux session(s).
Human re-authentication required (browser OAuth flow).

Affected sessions:
${AFFECTED_LIST}
Action: Open a browser, run 'claude login' in a local terminal, and restart affected sessions."

# --- Record Result ------------------------------------------------------------

SUMMARY="claude-login-dog: ${#AFFECTED[@]} session(s) need re-auth: ${AFFECTED[*]}"

bd create "claude-login-dog: $SUMMARY" -t chore --ephemeral \
  -l "type:plugin-run,plugin:claude-login-dog,result:alert-sent" \
  -d "$SUMMARY" --silent 2>/dev/null || true
