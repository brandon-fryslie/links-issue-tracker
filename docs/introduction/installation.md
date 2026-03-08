# Installation

## Requirements

- Git repository or worktree
- Dolt CLI `>= 1.81.10`
- Go toolchain (for `go install`)

## Install `lk`

```sh
go install github.com/bmf/links-issue-tracker/cmd/lk@latest
```

## Enable shell completion (optional)

```sh
lk completion bash > ~/.local/share/bash-completion/completions/lk
lk completion zsh > ~/.zfunc/_lk
lk completion fish > ~/.config/fish/completions/lk.fish
```

## Install sync automation once per clone

```sh
lk hooks install
```

This installs a shared `pre-push` hook in your clone’s common Git dir so all worktrees inherit the same behavior.
