#!/usr/bin/env bash
# meerkat-backend/run.sh — Health check and auto-start for meerkat backend.
#
# Checks if the meerkat backend is responding on its health endpoint.
# If down, starts it using the meerkat rig's start.sh script.
#
# Usage: ./run.sh

set -euo pipefail

MEERKAT_PORT="${MEERKAT_PORT:-8080}"
MEERKAT_RIG="/gt/meerkat/mayor/rig"
START_SCRIPT="$MEERKAT_RIG/scripts/start.sh"
PID_FILE="$MEERKAT_RIG/.runtime/meerkat.pid"
HEALTH_URL="http://127.0.0.1:${MEERKAT_PORT}/api/health"

log() {
  echo "[meerkat-backend] $*"
}

# Check if backend is responding
if curl -sf --max-time 5 "$HEALTH_URL" > /dev/null 2>&1; then
  log "Backend healthy on port $MEERKAT_PORT"
  exit 0
fi

log "Backend not responding on port $MEERKAT_PORT"

# Check if PID file exists but process is dead (stale PID)
if [ -f "$PID_FILE" ]; then
  OLD_PID=$(cat "$PID_FILE")
  if kill -0 "$OLD_PID" 2>/dev/null; then
    log "Process $OLD_PID exists but not responding — health check failed"
    log "Killing stale process before restart..."
    kill "$OLD_PID" 2>/dev/null || true
    sleep 2
    if kill -0 "$OLD_PID" 2>/dev/null; then
      kill -9 "$OLD_PID" 2>/dev/null || true
      sleep 1
    fi
  fi
  rm -f "$PID_FILE"
fi

# Verify start script exists
if [ ! -x "$START_SCRIPT" ]; then
  log "ERROR: Start script not found or not executable: $START_SCRIPT"
  exit 1
fi

log "Starting meerkat backend..."
if "$START_SCRIPT"; then
  log "Backend started successfully"
  exit 0
else
  log "ERROR: Failed to start backend — check $MEERKAT_RIG/.runtime/meerkat.log"
  exit 1
fi
