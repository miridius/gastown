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

# Rebuild gt binary from source if the installed binary is stale (missing BuiltProperly ldflag).
# This self-heals stale Docker images without requiring a full image rebuild.
GT_RIG_SRC="/gt/gastown/mayor/rig"
if /app/gastown/gt version 2>&1 | grep -q "WARNING.*built with"; then
    if [ -f "$GT_RIG_SRC/Makefile" ] && command -v go >/dev/null 2>&1; then
        echo "Rebuilding gt binary (stale image detected)..."
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
