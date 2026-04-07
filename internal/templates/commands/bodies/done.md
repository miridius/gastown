# Done — Submit Work to Merge Queue

Signal that your work is complete and ready for the merge queue.

Arguments: $ARGUMENTS

## Pre-flight Checks

Before running `gt done`, verify your work is ready:

```bash
git status                          # Must be clean (no uncommitted changes)
git log --oneline origin/main..HEAD # Must have at least 1 commit
```

If there are uncommitted changes, commit them first:
```bash
git add <files>
git commit -m "<type>: <description>"
```

## Execute

Run `gt done` with any provided arguments:

```bash
gt done $ARGUMENTS
```

**Common usage:**
- `gt done` — Submit completed work (default: --status COMPLETED)
- `gt done --pre-verified` — Submit with pre-verification (you ran gates after rebase)
- `gt done --status ESCALATED` — Signal blocker, skip MR
- `gt done --status DEFERRED` — Pause work, skip MR

**If the bead has nothing to implement** (already fixed, can't reproduce):

⚠️ You MUST empirically verify before closing — "looks correct" is not proof.

```bash
# 1. Exercise the feature/bug scenario and confirm it works
# 2. Document evidence:
bd update <issue-id> --notes "Verified: <what you did and observed>"
# 3. Close with evidence:
bd close <issue-id> --reason="no-changes: verified — <explanation>"
# 4. Done:
gt done --empirically-verified --cleanup-status clean
```

This command pushes your branch, submits an MR to the merge queue, and transitions
you to IDLE. The Refinery handles the actual merge. You are done after this.
