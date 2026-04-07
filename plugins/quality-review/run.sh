#!/usr/bin/env bash
# quality-review/run.sh — Analyze quality-review trends and alert on breaches.
#
# Runs every 6h during Deacon patrol. Queries quality-review result wisps
# recorded by the Refinery, computes per-worker score trends, and alerts
# on quality breaches.
#
# Usage: ./run.sh [--window HOURS] [--dry-run]

set -euo pipefail

# --- Configuration -----------------------------------------------------------

WINDOW_HOURS=24
DRY_RUN=false

SCORE_OK_THRESHOLD="0.60"
SCORE_WARN_THRESHOLD="0.45"

# --- Argument parsing ---------------------------------------------------------

while [[ $# -gt 0 ]]; do
  case "$1" in
    --window)    WINDOW_HOURS="$2"; shift 2 ;;
    --dry-run)   DRY_RUN=true; shift ;;
    --help|-h)
      echo "Usage: $0 [--window HOURS] [--dry-run]"
      echo "  --window HOURS   Lookback window in hours (default: 24)"
      echo "  --dry-run        Report only, don't send alerts or escalations"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# --- Helpers ------------------------------------------------------------------

log() { echo "[quality-review] $*"; }

# --- Step 1: Query recent quality-review results -----------------------------

log "Querying quality-review results from last ${WINDOW_HOURS}h..."

RESULTS_JSON=$(bd list --json --all -l type:plugin-run,plugin:quality-review-result --created-after=-${WINDOW_HOURS}h 2>/dev/null) || RESULTS_JSON="[]"

RESULT_COUNT=$(printf '%s' "$RESULTS_JSON" | python3 -c "
import json, sys
data = json.load(sys.stdin)
print(len(data))
" 2>/dev/null) || RESULT_COUNT=0

if [[ "$RESULT_COUNT" -eq 0 ]]; then
  log "No quality-review results in last ${WINDOW_HOURS}h. Nothing to analyze."
  bd create "quality-review: No results in last ${WINDOW_HOURS}h" -t chore --ephemeral \
    -l type:plugin-run,plugin:quality-review,result:success \
    -d "No quality-review results in last ${WINDOW_HOURS}h. Nothing to analyze." \
    --silent 2>/dev/null || true
  exit 0
fi

log "Found $RESULT_COUNT quality-review results."

# --- Steps 2-4: Compute per-worker trends, classify, and alert ---------------

# Use python3 for JSON parsing and floating-point math.
ANALYSIS=$(printf '%s' "$RESULTS_JSON" | python3 -c "
import json, sys

data = json.load(sys.stdin)

# Parse per-worker data from labels.
workers = {}  # worker -> list of {score, recommendation, created_at}

for wisp in data:
    labels = wisp.get('labels', [])
    if isinstance(labels, str):
        labels = [l.strip() for l in labels.split(',')]

    worker = None
    rig = None
    score = None
    recommendation = None
    created_at = wisp.get('created_at', '')

    for label in labels:
        if label.startswith('worker:'):
            worker = label.split(':', 1)[1]
        elif label.startswith('rig:'):
            rig = label.split(':', 1)[1]
        elif label.startswith('score:'):
            try:
                score = float(label.split(':', 1)[1])
            except ValueError:
                pass
        elif label.startswith('recommendation:'):
            recommendation = label.split(':', 1)[1]

    if worker and score is not None:
        if worker not in workers:
            workers[worker] = {'rig': rig or 'unknown', 'entries': []}
        workers[worker]['entries'].append({
            'score': score,
            'recommendation': recommendation or 'unknown',
            'created_at': created_at,
        })

# Compute trends per worker.
results = []
for worker, info in sorted(workers.items()):
    entries = info['entries']
    rig = info['rig']
    count = len(entries)
    scores = [e['score'] for e in entries]
    avg = sum(scores) / count
    rejections = sum(1 for e in entries if e['recommendation'] == 'request_changes')
    rejection_rate = (rejections / count) * 100

    # Trend: compare first-half avg vs second-half avg.
    # Sort by created_at for temporal ordering.
    entries.sort(key=lambda e: e['created_at'])
    mid = count // 2
    if mid > 0 and count > 1:
        first_half = sum(e['score'] for e in entries[:mid]) / mid
        second_half = sum(e['score'] for e in entries[mid:]) / (count - mid)
        diff = second_half - first_half
        if diff > 0.05:
            trend = 'improving'
        elif diff < -0.05:
            trend = 'declining'
        else:
            trend = 'stable'
    else:
        trend = 'stable'

    # Classify.
    if avg >= 0.60:
        status = 'OK'
    elif avg >= 0.45:
        status = 'WARN'
    else:
        status = 'BREACH'

    results.append({
        'worker': worker,
        'rig': rig,
        'count': count,
        'avg': round(avg, 3),
        'rejection_rate': round(rejection_rate, 1),
        'trend': trend,
        'status': status,
    })

# Output as JSON for bash consumption.
json.dump(results, sys.stdout)
" 2>/dev/null) || {
  log "ERROR: Failed to analyze results"
  bd create "quality-review: FAILED" -t chore --ephemeral \
    -l type:plugin-run,plugin:quality-review,result:failure \
    -d "Failed to parse/analyze quality-review results." \
    --silent 2>/dev/null || true
  if ! $DRY_RUN; then
    gt escalate "Plugin FAILED: quality-review" \
      --severity medium \
      --reason "Failed to parse/analyze quality-review result wisps" 2>/dev/null || true
  fi
  exit 1
}

# --- Process analysis results -------------------------------------------------

WORKER_COUNT=$(printf '%s' "$ANALYSIS" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))" 2>/dev/null) || WORKER_COUNT=0
BREACH_COUNT=0
WARN_COUNT=0

log ""
log "=== Worker Quality Summary ==="

# Iterate over each worker result.
while IFS= read -r LINE; do
  [[ -z "$LINE" ]] && continue

  WORKER=$(printf '%s' "$LINE" | python3 -c "import json,sys; print(json.load(sys.stdin)['worker'])")
  RIG=$(printf '%s' "$LINE" | python3 -c "import json,sys; print(json.load(sys.stdin)['rig'])")
  COUNT=$(printf '%s' "$LINE" | python3 -c "import json,sys; print(json.load(sys.stdin)['count'])")
  AVG=$(printf '%s' "$LINE" | python3 -c "import json,sys; print(json.load(sys.stdin)['avg'])")
  REJECTION_RATE=$(printf '%s' "$LINE" | python3 -c "import json,sys; print(json.load(sys.stdin)['rejection_rate'])")
  TREND=$(printf '%s' "$LINE" | python3 -c "import json,sys; print(json.load(sys.stdin)['trend'])")
  STATUS=$(printf '%s' "$LINE" | python3 -c "import json,sys; print(json.load(sys.stdin)['status'])")

  log "  $WORKER ($RIG): avg=$AVG reviews=$COUNT rejection=${REJECTION_RATE}% trend=$TREND status=$STATUS"

  if [[ "$STATUS" == "BREACH" ]]; then
    BREACH_COUNT=$((BREACH_COUNT + 1))
    if ! $DRY_RUN; then
      gt mail send mayor/ -s "Quality BREACH: $WORKER" --stdin <<BODY
Worker: $WORKER
Rig: $RIG
Avg Score: $AVG
Reviews: $COUNT
Rejection Rate: ${REJECTION_RATE}%
Trend: $TREND

Action: Review recent merges from this worker for quality issues.
BODY

      gt escalate "Quality BREACH: $WORKER (avg: $AVG)" \
        --severity medium \
        --reason "Worker $WORKER in rig $RIG has avg quality score $AVG over $COUNT reviews" 2>/dev/null || true
    else
      log "  [dry-run] Would alert on BREACH for $WORKER"
    fi
  elif [[ "$STATUS" == "WARN" ]]; then
    WARN_COUNT=$((WARN_COUNT + 1))
  fi
done < <(printf '%s' "$ANALYSIS" | python3 -c "
import json, sys
for item in json.load(sys.stdin):
    print(json.dumps(item))
")

# --- Step 5: Record run result ------------------------------------------------

log ""
log "=== Quality Review Complete ==="
log "  Workers analyzed: $WORKER_COUNT"
log "  Results reviewed: $RESULT_COUNT"
log "  Breaches: $BREACH_COUNT"
log "  Warnings: $WARN_COUNT"

SUMMARY="quality-review: Analyzed $WORKER_COUNT workers over $RESULT_COUNT reviews. $BREACH_COUNT breaches, $WARN_COUNT warnings."
log "$SUMMARY"

bd create "quality-review: Analyzed $WORKER_COUNT workers over $RESULT_COUNT reviews" -t chore --ephemeral \
  -l type:plugin-run,plugin:quality-review,result:success \
  -d "$SUMMARY" \
  --silent 2>/dev/null || true

log "Done."
