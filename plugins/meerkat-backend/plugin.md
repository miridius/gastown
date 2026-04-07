+++
name = "meerkat-backend"
description = "Monitor and auto-start the meerkat backend server"
version = 1

[gate]
type = "cooldown"
duration = "1m"

[tracking]
labels = ["plugin:meerkat-backend", "category:infrastructure"]
digest = true

[execution]
timeout = "3m"
notify_on_failure = true
severity = "medium"
+++

# Meerkat Backend

Monitors the meerkat backend health endpoint and auto-starts it if down.
Uses the existing `start.sh` script from the meerkat rig.

## What it does

1. Checks if the meerkat backend is responding on its configured port (default 8080)
2. If healthy: reports OK, exits
3. If down: runs the meerkat `start.sh` to bring it back up
4. Reports start result (success or failure)

## Configuration

- Port: controlled by `MEERKAT_PORT` env var (default: 8080)
- Start script: `/gt/meerkat/mayor/rig/scripts/start.sh`
