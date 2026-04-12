+++
name = "claude-login-dog"
description = "Detect Claude OAuth expiry in tmux panes and alert mayor for human re-authentication"
version = 1

[gate]
type = "cooldown"
duration = "5m"

[tracking]
labels = ["plugin:claude-login-dog", "category:health"]
digest = true

[execution]
timeout = "30s"
notify_on_failure = true
severity = "high"
+++

# Claude Login Dog

Detects when a Claude Code session is stuck at an OAuth login/re-authentication
prompt. When the OAuth token expires overnight (or at any time), headless agent
sessions stall silently because no human is present to complete the browser-based
OAuth flow.

This is a **shell-only plugin** — no LLM calls needed. Pattern matching is
sufficient to detect login prompts.

**Design principle**: Detect and alert only. Auto re-authentication is out of
scope (requires human action in a browser).

## How It Works

1. Enumerate all active tmux sessions
2. Capture the last 15 lines of each pane
3. Match against Claude Code-specific login/auth prompt patterns
4. If detected: escalate to mayor with affected session details

## Detection Patterns

Patterns are specific to Claude Code authentication prompts to avoid false
positives from agents discussing auth-related code:

| Pattern | Signal |
|---------|--------|
| `console.anthropic.com` | OAuth redirect URL (strongest) |
| `claude.ai/login` | Login redirect URL |
| `How would you like to authenticate` | CLI auth TUI prompt |
| `Authentication expired` | Token expiry message |
| `OAuth token.*expired` | Token status message |
| `OAuth token revoked` | Token revoked message |
| `open this URL.*sign in` | Browser auth flow prompt |

## Step 1: Enumerate tmux sessions

List all active tmux sessions. Any session running Claude Code can be affected
by OAuth expiry — we check all of them.

```bash
echo "=== Claude Login Dog: Scanning for OAuth expiry ==="

# List all tmux sessions
SESSIONS=$(tmux list-sessions -F '#{session_name}' 2>/dev/null || true)
if [ -z "$SESSIONS" ]; then
  echo "No tmux sessions found — nothing to check"
  exit 0
fi

SESSION_COUNT=$(echo "$SESSIONS" | wc -l | tr -d ' ')
echo "Found $SESSION_COUNT tmux sessions"
```

## Step 2: Scan each session for login prompts

Capture pane output and match against login-specific patterns.
Patterns are deliberately specific to Claude Code prompts, not generic auth terms.

```bash
# Login/auth prompt patterns (extended regex, case-insensitive)
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
```

## Step 3: Alert on detection

If any sessions are stuck at a login prompt, escalate to mayor.
Deduplicate: a single OAuth expiry typically affects all sessions,
so we send ONE escalation listing all affected sessions.

```bash
if [ ${#AFFECTED[@]} -eq 0 ]; then
  echo "=== All clear — no OAuth expiry detected ==="
  exit 0
fi

# Build the affected sessions list
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
```

## Record Result

```bash
if [ ${#AFFECTED[@]} -gt 0 ]; then
  SUMMARY="claude-login-dog: ${#AFFECTED[@]} session(s) need re-auth: ${AFFECTED[*]}"
  RESULT_LABEL="result:alert-sent"
else
  SUMMARY="claude-login-dog: all clear"
  RESULT_LABEL="result:success"
fi

bd create "claude-login-dog: $SUMMARY" -t chore --ephemeral \
  -l "type:plugin-run,plugin:claude-login-dog,$RESULT_LABEL" \
  -d "$SUMMARY" --silent 2>/dev/null || true
```
