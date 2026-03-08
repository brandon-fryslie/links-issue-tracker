# links

`links` is a small, worktree-native issue tracker with a flat CLI: `lk`.

## Install

```sh
go install github.com/bmf/links-issue-tracker/cmd/lk@latest
```

Shell completion:

```sh
lk completion bash > ~/.local/share/bash-completion/completions/lk
lk completion zsh > ~/.zfunc/_lk
lk completion fish > ~/.config/fish/completions/lk.fish
```

## Design

- `// [LAW:one-source-of-truth]` One canonical Dolt database per git clone, stored under the shared git common dir.
- `// [LAW:single-enforcer]` The `lk` CLI is the only write boundary.
- `// [LAW:no-silent-fallbacks]` Running outside a git repo is an explicit error.

The live database and workspace config live under:

```txt
$(git rev-parse --git-common-dir)/links/
```

All worktrees in the same clone therefore share one stable, current view of work items.

## Commands

```txt
lk new --title <title> [--description <text>] [--type task|feature|bug|chore|epic] [--priority 0-4] [--assignee <user>] [--labels a,b] [--expected-revision N] [--json]
lk ls [--status open|closed] [--type <type>] [--assignee <user>] [--priority-min N] [--priority-max N] [--search <text>] [--ids a,b] [--labels a,b] [--has-comments] [--include-archived] [--include-deleted] [--updated-after RFC3339] [--updated-before RFC3339] [--query <expr>] [--sort field:asc|desc,...] [--columns id,state,title,...] [--format lines|table] [--limit N] [--json]
lk show <id> [--json]
lk edit <id> [--title ...] [--description ...] [--type ...] [--priority ...] [--assignee ...|--clear-assignee] [--labels a,b|--clear-labels] [--expected-revision N] [--json]
lk close <id> --reason <text> [--by <user>] [--expected-revision N] [--json]
lk open <id> --reason <text> [--by <user>] [--expected-revision N] [--json]
lk archive <id> --reason <text> [--by <user>] [--expected-revision N] [--json]
lk delete <id> --reason <text> [--by <user>] [--expected-revision N] [--json]
lk unarchive <id> --reason <text> [--by <user>] [--expected-revision N] [--json]
lk restore <id> --reason <text> [--by <user>] [--expected-revision N] [--json]
lk comment add <id> --body <text> [--by <user>] [--expected-revision N] [--json]
lk label add <issue-id> <label> [--by <user>] [--expected-revision N] [--json]
lk label rm <issue-id> <label> [--expected-revision N] [--json]
lk parent set <child-id> <parent-id> [--by <user>] [--expected-revision N] [--json]
lk parent clear <child-id> [--expected-revision N] [--json]
lk children <parent-id> [--json]
lk dep add <src-id> <dst-id> [--type blocks|parent-child|related-to] [--by <user>] [--expected-revision N] [--json]
lk dep rm <src-id> <dst-id> [--type blocks|parent-child|related-to] [--expected-revision N] [--json]
lk dep ls <issue-id> [--type blocks|parent-child|related-to] [--json]
lk export [--json]
lk sync export [--path <path>] [--force] [--json]
lk sync import [--path <path>] [--resolve fail|local|remote] [--force] [--json]
lk sync status [--path <path>] [--json]
lk doctor [--json]
lk fsck [--repair] [--json]
lk backup create [--keep N] [--json]
lk backup list [--json]
lk backup restore (--path <snapshot.json> | --latest) [--force] [--json]
lk recover (--from-sync <path> | --from-backup <path> | --latest-backup) [--force] [--json]
lk bulk label <add|rm> --ids a,b --label <name> [--by <user>] [--expected-revision N] [--json]
lk bulk <close|archive> --ids a,b --reason <text> [--by <user>] [--expected-revision N] [--json]
lk bulk import --path <export.json> [--force] [--json]
lk quickstart [--json]
lk beads import --db <path> [--json]
lk beads export --db <path> [--json]
lk workspace [--json]
```

## Agent quickstart

- `lk quickstart` prints operational guidance for agents.
- `lk quickstart --json` emits structured instructions suitable for machine consumption.

## Querying

- `// [LAW:one-source-of-truth]` Filtering semantics are owned by the store query contract; the CLI query language lowers into that same filter shape.
- `lk ls --query 'status:open type:task priority<=2 has:comments renderer contract'`
- Supported query terms:
  - `status:open|closed`
  - `type:task|feature|bug|chore|epic`
  - `assignee:<user>`
  - `id:<issue-id>`
  - `label:<name>`
  - `priority:<n>`
  - `priority<=<n>`, `priority>=<n>`, `p<=<n>`, `p>=<n>`
  - `updated>=<RFC3339>`, `updated<=<RFC3339>`
  - `has:comments`
  - any other term becomes title/description text search

## Labels

- `// [LAW:one-source-of-truth]` Labels are stored canonically in the `labels` table and projected onto issues for listing, querying, and export.
- Labels are first-class writable data:
  - set on create with `--labels`
  - replace on edit with `--labels`
  - clear on edit with `--clear-labels`
  - mutate incrementally with `lk label add` and `lk label rm`
- Labels are normalized to lowercase and must not contain commas.

## Sync and concurrency

- `// [LAW:single-enforcer]` The store owns one canonical `workspace_revision` and bumps it on every successful mutation.
- `lk workspace --json` exposes the current `workspace_revision` so agents can detect stale views before writing.
- Mutating commands support `--expected-revision N`; stale writes fail with a dedicated exit code.
- `lk sync export` writes an atomic snapshot to `links/export.json` by default.
- `lk sync export` refuses to overwrite a sync file that changed outside `links` unless `--force` is used.
- `lk sync import` performs a 3-way merge (`base`, `local`, `remote`) and reports per-issue conflicts.
- `lk sync import` conflict strategy is explicit: `--resolve fail|local|remote`.
- `lk sync import` refuses to replace local state if the workspace has unsynced local changes since the last recorded sync unless `--force` is used.
- The export snapshot includes `workspace_revision`, so sync state can be correlated deterministically.

## Health and recovery

- `lk doctor` runs non-mutating checks (`integrity_check`, FK issues, relation invariants, orphan history rows).
- `lk fsck --repair` applies safe repairs for known integrity faults.
- `lk backup create/list/restore` manages rotating JSON snapshots.
- `lk recover` restores from a sync export or backup snapshot.

## Exit codes

`lk` exits with stable machine-friendly codes:

- `0`: success
- `1`: generic failure
- `2`: usage error
- `3`: validation error
- `4`: not found
- `5`: conflict
- `6`: stale revision
- `7`: corruption/integrity failure

## Lifecycle history

- `// [LAW:one-source-of-truth]` Current issue state lives on the issue row; lifecycle reasons live in the `issue_history` table.
- `lk close`, `lk open`, `lk archive`, and `lk delete` all require `--reason`.
- `lk show` renders lifecycle history so reopen/archive/delete reasons are visible in one place.
- Active listings exclude archived and deleted issues by default; use `--include-archived` and `--include-deleted` when needed.

## Beads compatibility

- `// [LAW:locality-or-seam]` Beads compatibility is isolated in `internal/beads` so the core store keeps one canonical data model.
- `lk beads import` reads Beads data tables `issues`, `dependencies`, `comments`, and `labels` from a Dolt database target.
- `lk beads export` writes those same tables to a Dolt database target for interoperability.
- Only the implemented core relationship types are imported/exported: `blocks`, `parent-child`, and `related-to`.
