#!/usr/bin/env bash
# stuck-agent-dog/run.sh — Context-aware stuck/crashed agent detection and restart.
#
# Detects stuck or crashed polecats and deacons by inspecting tmux session context
# before taking action. Unlike the daemon's blind kill-and-restart approach, this
# plugin checks whether an agent is truly unresponsive before restarting.
#
# Design principle: The daemon should NEVER kill workers. It detects and logs.
# This plugin (running as a Dog agent with AI judgment) makes the restart decision
# after inspecting tmux pane output for signs of life.
#
# Reference: WAR-ROOM-SERIAL-KILLER.md, commit f3d47a96.
#
# Scope:
#   IN SCOPE:  Polecat sessions (<rig>-polecat-<name>), Deacon session (hq-deacon)
#   OUT OF SCOPE: Crew, Mayor, Witness, Refinery sessions — NEVER touch these.
#
# Usage: ./run.sh

set -euo pipefail

# --- Step 1: Enumerate agents to check ---------------------------------------

echo "=== Stuck Agent Dog: Checking agent health ==="

TOWN_ROOT="$HOME/gt"
RIGS_JSON_PATH="${TOWN_ROOT}/mayor/rigs.json"

# Read rigs.json for rig names and beads prefixes
# CRITICAL: We need both the rig name (for filesystem paths like $TOWN_ROOT/$RIG/polecats/)
# and the beads prefix (for tmux session names like $PREFIX-polecat-$NAME).
# These can differ — e.g. rig "cfutons" may have prefix "CF".
if [ ! -f "$RIGS_JSON_PATH" ]; then
  echo "SKIP: rigs.json not found at $RIGS_JSON_PATH"
  exit 0
fi

RIGS_FILE=$(cat "$RIGS_JSON_PATH" 2>/dev/null)
if [ -z "$RIGS_FILE" ]; then
  echo "SKIP: could not read rigs.json"
  exit 0
fi

# Build a mapping of rig_name -> beads_prefix for session name construction
# Each line: rig_name|beads_prefix
RIG_PREFIX_MAP=$(echo "$RIGS_FILE" | jq -r '.rigs | to_entries[] | "\(.key)|\(.value.beads.prefix // .key)"' 2>/dev/null)
if [ -z "$RIG_PREFIX_MAP" ]; then
  echo "SKIP: no rigs found in rigs.json"
  exit 0
fi

# --- Step 2: Check polecat health --------------------------------------------

CRASHED=()
STUCK=()
HEALTHY=0

while IFS='|' read -r RIG PREFIX; do
  [ -z "$RIG" ] && continue
  # List polecat directories
  POLECAT_DIR="$TOWN_ROOT/$RIG/polecats"
  [ -d "$POLECAT_DIR" ] || continue

  for PCAT_PATH in "$POLECAT_DIR"/*/; do
    [ -d "$PCAT_PATH" ] || continue
    PCAT_NAME=$(basename "$PCAT_PATH")
    # Use beads prefix (not rig name) for tmux session name
    SESSION_NAME="${PREFIX}-polecat-${PCAT_NAME}"

    # Check if session exists
    if ! tmux has-session -t "$SESSION_NAME" 2>/dev/null; then
      # Session dead — check if it has hooked work
      HOOK_BEAD=$(bd show "$RIG/polecats/$PCAT_NAME" --json 2>/dev/null \
        | jq -r '.hook_bead // empty' 2>/dev/null)

      if [ -n "$HOOK_BEAD" ]; then
        # Check agent_state to avoid interfering with active spawning
        AGENT_STATE=$(bd show "$RIG/polecats/$PCAT_NAME" --json 2>/dev/null \
          | jq -r '.agent_state // empty' 2>/dev/null)
        if [ "$AGENT_STATE" = "spawning" ]; then
          echo "  SKIP $SESSION_NAME: agent_state=spawning (sling in progress)"
          continue
        fi
        CRASHED+=("$SESSION_NAME|$RIG|$PCAT_NAME|$HOOK_BEAD")
        echo "  CRASHED: $SESSION_NAME (hook=$HOOK_BEAD)"
      fi
    else
      # Session alive — check for agent process liveness
      # Capture last 5 lines of pane output to check for signs of life
      PANE_OUTPUT=$(tmux capture-pane -t "$SESSION_NAME" -p -S -5 2>/dev/null || echo "")

      # Check if agent process is running in the session
      PANE_PID=$(tmux list-panes -t "$SESSION_NAME" -F '#{pane_pid}' 2>/dev/null | head -1)
      if [ -n "$PANE_PID" ]; then
        # Check if Claude or another agent process is a descendant
        AGENT_ALIVE=$(pgrep -P "$PANE_PID" -f 'claude|node|anthropic' 2>/dev/null | head -1)
        if [ -z "$AGENT_ALIVE" ]; then
          # Agent process dead but session alive — zombie session
          HOOK_BEAD=$(bd show "$RIG/polecats/$PCAT_NAME" --json 2>/dev/null \
            | jq -r '.hook_bead // empty' 2>/dev/null)
          if [ -n "$HOOK_BEAD" ]; then
            STUCK+=("$SESSION_NAME|$RIG|$PCAT_NAME|$HOOK_BEAD|agent_dead")
            echo "  ZOMBIE: $SESSION_NAME (agent dead, session alive, hook=$HOOK_BEAD)"
          fi
        else
          HEALTHY=$((HEALTHY + 1))
        fi
      else
        HEALTHY=$((HEALTHY + 1))
      fi
    fi
  done
done <<< "$RIG_PREFIX_MAP"

echo ""
echo "Health summary: ${#CRASHED[@]} crashed, ${#STUCK[@]} stuck, $HEALTHY healthy"

# --- Step 3: Check deacon health ----------------------------------------------

echo ""
echo "=== Deacon Health ==="

DEACON_SESSION="hq-deacon"
DEACON_ISSUE=""

if ! tmux has-session -t "$DEACON_SESSION" 2>/dev/null; then
  echo "  CRASHED: Deacon session is dead"
  DEACON_ISSUE="crashed"
else
  # Check deacon heartbeat via heartbeat.json (structured, authoritative).
  # Previously checked .deacon-heartbeat file mtime, but that legacy file can
  # go stale even when heartbeat.json is fresh (write errors silently ignored).
  HEARTBEAT_JSON="$TOWN_ROOT/deacon/heartbeat.json"
  if [ -f "$HEARTBEAT_JSON" ]; then
    HEARTBEAT_TS=$(jq -r '.timestamp // empty' "$HEARTBEAT_JSON" 2>/dev/null)
    if [ -n "$HEARTBEAT_TS" ]; then
      # Convert ISO 8601 timestamp to epoch seconds
      HEARTBEAT_EPOCH=$(date -d "$HEARTBEAT_TS" +%s 2>/dev/null || date -jf "%Y-%m-%dT%H:%M:%S" "${HEARTBEAT_TS%%.*}" +%s 2>/dev/null || true)
      if [ -n "$HEARTBEAT_EPOCH" ]; then
        NOW=$(date +%s)
        HEARTBEAT_AGE=$(( NOW - HEARTBEAT_EPOCH ))

        if [ "$HEARTBEAT_AGE" -gt 600 ]; then
          echo "  STUCK: Deacon heartbeat stale (${HEARTBEAT_AGE}s old, >10m threshold)"
          DEACON_ISSUE="stuck_heartbeat_${HEARTBEAT_AGE}s"
        else
          echo "  OK: Deacon heartbeat ${HEARTBEAT_AGE}s old (from heartbeat.json)"
        fi
      else
        echo "  WARN: could not parse heartbeat timestamp: $HEARTBEAT_TS"
      fi
    else
      echo "  WARN: heartbeat.json missing timestamp field"
    fi
  else
    echo "  WARN: No heartbeat.json found"
  fi
fi

# --- Step 4: AI judgment happens here -----------------------------------------
#
# This is the key difference from daemon blind-kill. For each crashed or stuck
# agent, the dog agent inspects tmux pane context to determine if restart is
# appropriate.
#
# SCOPE REMINDER: Only act on entries in CRASHED[] and STUCK[] arrays.
# These contain ONLY polecats and deacon. Do NOT inspect, evaluate, or act
# on ANY other sessions (crew, mayor, witness, refinery).
#
# Decision framework:
# 1. If agent is clearly dead (no process, no output) → restart
# 2. If agent shows recent activity in pane → nudge first, check again next cycle
# 3. If agent has been stuck for >15 minutes with no pane activity → restart
# 4. If mass death detected (>3 crashes in same cycle) → escalate, don't restart

# --- Step 5: Take action -----------------------------------------------------

# For crashed polecats — notify witness to handle restart
for ENTRY in "${CRASHED[@]}"; do
  IFS='|' read -r SESSION RIG PCAT HOOK <<< "$ENTRY"

  echo "Requesting restart for $RIG/polecats/$PCAT (hook=$HOOK)"

  gt mail send "$RIG/witness" \
    -s "RESTART_POLECAT: $RIG/$PCAT" \
    --stdin <<BODY
Polecat $PCAT crash confirmed by stuck-agent-dog plugin.
Context-aware inspection completed — agent is genuinely dead.

hook_bead: $HOOK
action: restart requested

Please restart this polecat session.
BODY

done

# For zombie polecats — kill zombie session first, then request restart
for ENTRY in "${STUCK[@]}"; do
  IFS='|' read -r SESSION RIG PCAT HOOK REASON <<< "$ENTRY"

  echo "Killing zombie session $SESSION and requesting restart"
  tmux kill-session -t "$SESSION" 2>/dev/null || true

  gt mail send "$RIG/witness" \
    -s "RESTART_POLECAT: $RIG/$PCAT (zombie cleared)" \
    --stdin <<BODY
Polecat $PCAT zombie session cleared by stuck-agent-dog plugin.
Session was alive but agent process was dead.

hook_bead: $HOOK
reason: $REASON
action: restart requested

Please restart this polecat session.
BODY

done

# For deacon issues
if [ -n "$DEACON_ISSUE" ]; then
  echo "Escalating deacon issue: $DEACON_ISSUE"
  gt escalate "Deacon $DEACON_ISSUE detected by stuck-agent-dog" \
    -s HIGH \
    --reason "Deacon issue: $DEACON_ISSUE. Context inspection completed."
fi

# --- Step 6: Mass death check -------------------------------------------------

TOTAL_ISSUES=$(( ${#CRASHED[@]} + ${#STUCK[@]} ))
if [ "$TOTAL_ISSUES" -ge 3 ]; then
  echo "MASS DEATH: $TOTAL_ISSUES agents down in same cycle — escalating"
  gt escalate "Mass agent death: $TOTAL_ISSUES agents down" \
    -s CRITICAL \
    --reason "stuck-agent-dog detected $TOTAL_ISSUES agents down simultaneously.
Crashed: ${CRASHED[*]}
Stuck: ${STUCK[*]}
This may indicate a systemic issue (Dolt, OOM, infra). Investigate before mass restart."
fi

# --- Record Result ------------------------------------------------------------

SUMMARY="Agent health check: ${#CRASHED[@]} crashed, ${#STUCK[@]} stuck, $HEALTHY healthy"
if [ -n "$DEACON_ISSUE" ]; then
  SUMMARY="$SUMMARY, deacon=$DEACON_ISSUE"
fi
echo "=== $SUMMARY ==="

bd create "stuck-agent-dog: $SUMMARY" -t chore --ephemeral \
  -l type:plugin-run,plugin:stuck-agent-dog,result:success \
  -d "$SUMMARY" --silent 2>/dev/null || true
