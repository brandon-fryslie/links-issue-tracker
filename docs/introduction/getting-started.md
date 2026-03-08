# Getting started

## 1. Verify workspace state

```sh
lk workspace --json
```

## 2. Create your first issue

```sh
lk new --title "First task" --type task --priority 2 --json
```

## 3. List and inspect

```sh
lk ls --json
lk show <issue-id> --json
```

## 4. Connect remotes (Git is canonical)

```sh
git remote -v
lk sync remote ls --json
```

## 5. Pull/push issue state

```sh
lk sync pull --remote origin --branch main
# ...make lk changes...
lk sync push --remote origin --branch main
```

## 6. Health check

```sh
lk doctor --json
```
