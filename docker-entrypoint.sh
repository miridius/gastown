#!/bin/sh
set -e

# Re-apply git/dolt config on every start so env var changes take effect
# even when the home volume already exists from a previous run.
if [ -n "$GIT_USER" ] && [ -n "$GIT_EMAIL" ]; then
    git config --global user.name "$GIT_USER"
    git config --global user.email "$GIT_EMAIL"
    git config --global credential.helper store
    dolt config --global --add user.name "$GIT_USER"
    dolt config --global --add user.email "$GIT_EMAIL"
fi

if [ ! -f /gt/mayor/town.json ]; then
    echo "Initializing Gas Town workspace at /gt..."
    /app/gastown/gt install /gt --git
else
    echo "Refreshing Gas Town workspace at /gt..."
    /app/gastown/gt install /gt --git --force
fi

# Rebuild gt binary from source if the installed binary is stale.
# Triggers on: missing BuiltProperly ldflag (go build warning) OR version drift
# vs the source tree. This self-heals stale Docker images without requiring a
# full image rebuild.
GT_RIG_SRC="/gt/gastown/mayor/rig"
NEED_REBUILD=false

# Check 1: Missing BuiltProperly ldflag (shows "go build" warning)
if /app/gastown/gt version 2>&1 | grep -q "WARNING.*built with"; then
    echo "Self-heal: stale binary detected (missing ldflag)."
    NEED_REBUILD=true
fi

# Check 2: Version drift — compare baked-in commit to source HEAD
if [ "$NEED_REBUILD" = "false" ] && { [ -d "$GT_RIG_SRC/.git" ] || [ -f "$GT_RIG_SRC/../.git" ]; }; then
    BAKED_COMMIT=$(/app/gastown/gt version 2>/dev/null | grep -o '@[a-f0-9]*' | head -1 | tr -d '@')
    SOURCE_COMMIT=$(cd "$GT_RIG_SRC" && git rev-parse --short HEAD 2>/dev/null)
    if [ -n "$BAKED_COMMIT" ] && [ -n "$SOURCE_COMMIT" ] && [ "$BAKED_COMMIT" != "$SOURCE_COMMIT" ]; then
        echo "Self-heal: version drift detected (baked=$BAKED_COMMIT, source=$SOURCE_COMMIT)."
        NEED_REBUILD=true
    fi
fi

if [ "$NEED_REBUILD" = "true" ]; then
    if [ -f "$GT_RIG_SRC/Makefile" ] && command -v go >/dev/null 2>&1; then
        echo "Rebuilding gt binary from source..."
        if (cd "$GT_RIG_SRC" && make build >/dev/null 2>&1); then
            # Atomic replace: cp+mv avoids "Text file busy" if binary is running
            for bin in gt gt-proxy-server gt-proxy-client; do
                cp "$GT_RIG_SRC/$bin" "/app/gastown/$bin.new" && mv "/app/gastown/$bin.new" "/app/gastown/$bin"
            done
            echo "gt binary rebuilt successfully."
        else
            echo "Warning: gt rebuild failed, continuing with stale binary."
        fi
    fi
fi

exec "$@"
