# links

`links` is a worktree-native issue tracker with a flat CLI: `lk`.

## Quickstart

Requirements:
- Git repository/worktree
- Dolt CLI `>= 1.81.10`

Install:

```sh
go install github.com/bmf/links-issue-tracker/cmd/lk@latest
```

Initialize in your repo and install sync automation:

```sh
lk hooks install
git remote -v
lk sync remote ls --json
```

Create and inspect work:

```sh
lk new --title "First task" --type task --priority 2 --json
lk ls --json
lk show <issue-id> --json
```

Push/pull DB changes through Dolt remotes mirrored from Git remotes:

```sh
lk sync pull --remote origin --branch main
# ...make lk changes...
lk sync push --remote origin --branch main
```

Useful commands:

```sh
lk quickstart --json
lk workspace --json
lk doctor --json
```

## More docs

- Docs index (recommended start): [docs/index.md](docs/index.md)
- Sync and remote behavior: [docs/dolt-remote-sync.md](docs/dolt-remote-sync.md)
- Full command reference: `lk help`
- Agent-focused workflow: `lk quickstart` / `lk quickstart --json`
