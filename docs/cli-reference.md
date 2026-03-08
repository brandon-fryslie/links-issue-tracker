# CLI reference

For full syntax, run:

```sh
lk help
```

## Common commands

### Create/list/show

```sh
lk new --title "..." --type task --priority 2 --json
lk ls --json
lk show <issue-id> --json
```

### Edit and lifecycle

```sh
lk edit <issue-id> --title "..." --json
lk close <issue-id> --reason "..." --json
lk open <issue-id> --reason "..." --json
lk archive <issue-id> --reason "..." --json
lk delete <issue-id> --reason "..." --json
```

### Relationships and labels

```sh
lk parent set <child-id> <parent-id> --json
lk dep add <src-id> <dst-id> --type related-to --json
lk label add <issue-id> <label> --json
```

### Sync and automation

```sh
lk hooks install
lk sync status --json
lk sync pull --remote origin --branch main
lk sync push --remote origin --branch main
```

### Health and recovery

```sh
lk doctor --json
lk fsck --repair --json
lk backup create --json
lk backup restore --latest --json
```
