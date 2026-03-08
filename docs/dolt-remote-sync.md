# Dolt Remote Sync

`links` sync is Dolt-native and uses Dolt git-remote support directly.
Git remotes are the canonical remote configuration.

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
lk hooks install
git remote add origin https://github.com/<org>/<repo>.git
lk sync remote ls --json
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
- `lk sync fetch [--remote <name>] [--prune] [--json]`
- `lk sync pull [--remote <name>] [--branch <name>] [--json]`
- `lk sync push [--remote <name>] [--branch <name>] [--set-upstream] [--force] [--json]`

Before each `lk sync` command, `lk` reconciles Dolt remotes to exactly match `git remote -v` fetch URLs:

- add missing Dolt remotes
- update changed remote URLs
- remove Dolt remotes that no longer exist in Git

## Push automation

`lk hooks install` writes `$(git rev-parse --git-common-dir)/hooks/pre-push` and chains any existing user hook.
The hook auto-runs `lk sync push` for pushed branches, never blocks the git push, and emits one yellow warning line if DB sync fails.
