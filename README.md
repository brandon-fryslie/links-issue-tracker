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
