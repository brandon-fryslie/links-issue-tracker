# links

`links` is a small, worktree-native issue tracker with a flat CLI: `lk`.

## Design

- `// [LAW:one-source-of-truth]` One canonical SQLite database per git clone, stored under the shared git common dir.
- `// [LAW:single-enforcer]` The `lk` CLI is the only write boundary.
- `// [LAW:no-silent-fallbacks]` Running outside a git repo is an explicit error.

The live database and workspace config live under:

```txt
$(git rev-parse --git-common-dir)/links/
```

All worktrees in the same clone therefore share one stable, current view of work items.

## Commands

```txt
lk new --title <title> [--description <text>] [--type task|feature|bug|chore|epic] [--priority 0-4] [--assignee <user>] [--labels a,b] [--json]
lk ls [--status open|closed] [--type <type>] [--assignee <user>] [--priority-min N] [--priority-max N] [--search <text>] [--ids a,b] [--labels a,b] [--has-comments] [--updated-after RFC3339] [--updated-before RFC3339] [--query <expr>] [--limit N] [--json]
lk show <id> [--json]
lk edit <id> [--title ...] [--description ...] [--type ...] [--status ...] [--priority ...] [--assignee ...|--clear-assignee] [--labels a,b|--clear-labels] [--json]
lk close <id> [--json]
lk open <id> [--json]
lk comment add <id> --body <text> [--by <user>] [--json]
lk label add <issue-id> <label> [--by <user>] [--json]
lk label rm <issue-id> <label> [--json]
lk dep add <src-id> <dst-id> [--type blocks|parent-child|related-to] [--by <user>] [--json]
lk dep rm <src-id> <dst-id> [--type blocks|parent-child|related-to]
lk export
lk beads import --db <path> [--json]
lk beads export --db <path> [--json]
lk workspace [--json]
```

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

## Beads compatibility

- `// [LAW:locality-or-seam]` Beads compatibility is isolated in `internal/beads` so the core store keeps one canonical data model.
- `lk beads import` reads Beads' SQLite tables `issues`, `dependencies`, `comments`, and `labels`.
- `lk beads export` writes a Beads-compatible SQLite database with those same core tables populated from Links data.
- Only the implemented core relationship types are imported/exported: `blocks`, `parent-child`, and `related-to`.
