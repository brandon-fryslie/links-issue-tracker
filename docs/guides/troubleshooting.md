# Troubleshooting

## `links requires running inside a git repository/worktree`

Run commands from a repo/worktree directory.

## `dolt <version>+ is required`

Upgrade Dolt to `>= 1.81.10`.

## Sync warning on push hook

The hook is warn-only and never blocks push. Retry manually:

```sh
lk sync push --remote origin --branch <branch>
```

Then check status:

```sh
lk sync status --json
```

## Integrity errors

Run:

```sh
lk doctor --json
lk fsck --repair --json
```

## Unexpected state after import/restore

Use backups:

```sh
lk backup list --json
lk backup restore --latest --json
```
