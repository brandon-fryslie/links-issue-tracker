# Dolt Remote Sync

`links` sync is Dolt-native and uses Dolt git-remote support directly.

## Version requirement

- Required Dolt version: `>= 1.81.10`
- Enforced at app startup through `internal/doltcli.RequireMinimumVersion`.

## Local data location

The Links Dolt database is shared across all worktrees in the same clone:

```txt
$(git rev-parse --git-common-dir)/links/dolt
```

`lk sync` commands run in the current repo/worktree root and operate on that database.

## Typical setup

```sh
lk sync remote add --name origin --url https://github.com/<org>/<repo>.git
lk sync remote ls
lk sync fetch --remote origin
lk sync pull --remote origin --branch main
```

## Daily workflow

```sh
lk sync status
lk sync pull --remote origin --branch main
# ...work with lk commands...
lk sync push --remote origin --branch main
```

## Commands

- `lk sync status [--json]`
- `lk sync remote ls [--json]`
- `lk sync remote add --name <name> --url <url> [--json]`
- `lk sync remote rm --name <name> [--json]`
- `lk sync fetch [--remote <name>] [--prune] [--json]`
- `lk sync pull [--remote <name>] [--branch <name>] [--json]`
- `lk sync push [--remote <name>] [--branch <name>] [--set-upstream] [--force] [--json]`

