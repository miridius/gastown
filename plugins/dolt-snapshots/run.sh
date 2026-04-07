#!/usr/bin/env bash
# dolt-snapshots/run.sh — Build and run the dolt-snapshots binary.
#
# Builds the Go binary if missing or stale, runs a one-shot catch-up pass,
# then starts a background watcher that tails ~/.events.jsonl for convoy
# events and snapshots immediately (<1s latency vs ~60s polling).
#
# The watcher is managed via a PID file — only one instance runs at a time.
#
# Usage: ./run.sh

set -euo pipefail

PLUGIN_DIR="$(cd "$(dirname "$0")" && pwd)"
PIDFILE="$PLUGIN_DIR/.snapshot.pid"

# --- If watcher already running, skip ----------------------------------------

if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
  echo "Snapshot watcher already running (PID $(cat "$PIDFILE"))"
  exit 0
fi

# --- Build if binary missing or source is newer ------------------------------

if [ ! -f "$PLUGIN_DIR/snapshot" ] || [ "$PLUGIN_DIR/main.go" -nt "$PLUGIN_DIR/snapshot" ]; then
  echo "Building dolt-snapshots binary..."
  cd "$PLUGIN_DIR" && go build -o snapshot . 2>&1
  if [ $? -ne 0 ]; then
    echo "FATAL: Go build failed"
    exit 1
  fi
fi

# --- One-shot catch-up (process anything missed while watcher was down) ------

"$PLUGIN_DIR/snapshot" --cleanup --routes "$HOME/gt/.beads/routes.jsonl"
SNAPSHOT_EXIT=$?

if [ $SNAPSHOT_EXIT -ne 0 ]; then
  echo "Snapshot catch-up exited with code $SNAPSHOT_EXIT"
fi

# --- Start watcher in background (sub-second response to convoy events) ------

nohup "$PLUGIN_DIR/snapshot" --watch --routes "$HOME/gt/.beads/routes.jsonl" \
  >> "$PLUGIN_DIR/.snapshot.log" 2>&1 &
echo $! > "$PIDFILE"
echo "Snapshot watcher started (PID $!)"

# --- Record result -----------------------------------------------------------

RESULT="success"
if [ $SNAPSHOT_EXIT -ne 0 ]; then
  RESULT="failure"
fi

bd create "dolt-snapshots: $RESULT" -t chore --ephemeral \
  -l type:plugin-run,plugin:dolt-snapshots,result:$RESULT \
  -d "dolt-snapshots plugin completed with exit code $SNAPSHOT_EXIT. Watcher started." --silent 2>/dev/null || true
