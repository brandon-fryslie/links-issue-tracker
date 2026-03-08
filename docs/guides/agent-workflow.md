# Agent workflow

## Baseline loop

1. Read workspace context:
   - `lk workspace --json`
2. Find work:
   - `lk ls --query "status:open" --json`
3. Mutate safely:
   - include `--expected-revision` on writes
4. Sync:
   - `lk sync pull ...` before work
   - `lk sync push ...` after work

## Required assumptions for reliable automation

- hooks are installed once (`lk hooks install`)
- remote names come from Git (`git remote -v`)
- retries are attempted before escalating user-visible failures

## Machine-readable mode

Prefer `--json` on read and write commands for deterministic parsing.
