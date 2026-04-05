+++
name = "upstream-sync-dog"
description = "Sync forked rigs with their upstream repositories weekly"
version = 1

[gate]
type = "cooldown"
duration = "168h"

[tracking]
labels = ["plugin:upstream-sync-dog", "category:maintenance"]
digest = true

[execution]
type = "script"
timeout = "15m"
notify_on_failure = true
severity = "medium"
+++

# Upstream Sync Dog

Automatically syncs forked rigs with their upstream repositories.

Uses `gt upstream sync` to:
1. Fetch upstream changes
2. Merge into a sync branch
3. Run the full test suite
4. Push to origin if tests pass
5. Report conflicts or test failures for human resolution

## Schedule

Runs weekly (168h cooldown). Can also be triggered manually:
```bash
gt dog dispatch upstream-sync-dog
```

## What happens on failure

- **Merge conflicts**: A bead is filed with conflict details for human resolution
- **Test failures**: A bead is filed with test output for investigation
- **Both cases**: The sync branch is NOT pushed; origin/main stays clean

## Customisation review

Each sync also checks whether local fork commits overlap with upstream changes.
If upstream modified the same files as a local commit, it flags the overlap —
the local change may now be redundant or need updating.
