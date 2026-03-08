package cli

const bashCompletionScript = `# bash completion for lk
_lk_completions() {
  local current prev words cword
  _init_completion || return

  local commands="new ls show edit close open archive delete unarchive restore comment label parent children dep export sync doctor fsck backup recover bulk beads workspace hooks quickstart completion help"
  local comment_subcommands="add"
  local label_subcommands="add rm"
  local parent_subcommands="set clear"
  local dep_subcommands="add rm ls"
  local sync_subcommands="status remote fetch pull push"
  local sync_remote_subcommands="ls"
  local backup_subcommands="create list restore"
  local bulk_subcommands="label close archive import"
  local beads_subcommands="import export"
  local hooks_subcommands="install"

  case "${prev}" in
    lk)
      COMPREPLY=( $(compgen -W "${commands}" -- "${current}") )
      return
      ;;
    comment)
      COMPREPLY=( $(compgen -W "${comment_subcommands}" -- "${current}") )
      return
      ;;
    label)
      COMPREPLY=( $(compgen -W "${label_subcommands}" -- "${current}") )
      return
      ;;
    parent)
      COMPREPLY=( $(compgen -W "${parent_subcommands}" -- "${current}") )
      return
      ;;
    dep)
      COMPREPLY=( $(compgen -W "${dep_subcommands}" -- "${current}") )
      return
      ;;
    sync)
      COMPREPLY=( $(compgen -W "${sync_subcommands}" -- "${current}") )
      return
      ;;
    remote)
      if [[ "${words[1]}" == "sync" ]]; then
        COMPREPLY=( $(compgen -W "${sync_remote_subcommands}" -- "${current}") )
        return
      fi
      ;;
    backup)
      COMPREPLY=( $(compgen -W "${backup_subcommands}" -- "${current}") )
      return
      ;;
    bulk)
      COMPREPLY=( $(compgen -W "${bulk_subcommands}" -- "${current}") )
      return
      ;;
    beads)
      COMPREPLY=( $(compgen -W "${beads_subcommands}" -- "${current}") )
      return
      ;;
    hooks)
      COMPREPLY=( $(compgen -W "${hooks_subcommands}" -- "${current}") )
      return
      ;;
    completion)
      COMPREPLY=( $(compgen -W "bash zsh fish" -- "${current}") )
      return
      ;;
  esac

  COMPREPLY=( $(compgen -W "${commands}" -- "${current}") )
}

complete -F _lk_completions lk
`

const zshCompletionScript = `#compdef lk

_lk() {
  local -a commands
  commands=(
    'new:create issue'
    'ls:list issues'
    'show:show issue'
    'edit:edit issue'
    'close:close issue'
    'open:reopen issue'
    'archive:archive issue'
    'delete:delete issue'
    'unarchive:unarchive issue'
    'restore:restore deleted issue'
    'comment:add comment'
    'label:manage labels'
    'parent:manage parent relationship'
    'children:list child issues'
    'dep:manage dependencies'
    'export:write JSON export to stdout'
    'sync:sync via dolt remotes'
    'doctor:database health check'
    'fsck:database integrity check and repair'
    'backup:snapshot management'
    'recover:restore from backup or sync file'
    'bulk:bulk issue operations'
    'beads:beads dolt import export'
    'workspace:show workspace info'
    'hooks:install links git hooks'
    'quickstart:agent usage guide'
    'completion:emit shell completion'
    'help:show help'
  )

  local context state state_descr line
  _arguments '1:command:->command' '2:subcommand:->subcommand'

  case $state in
    command)
      _describe 'command' commands
      ;;
    subcommand)
      case $line[1] in
        comment)
          _values 'comment commands' add
          ;;
        label)
          _values 'label commands' add rm
          ;;
        parent)
          _values 'parent commands' set clear
          ;;
        dep)
          _values 'dep commands' add rm ls
          ;;
        sync)
          _values 'sync commands' status remote fetch pull push
          ;;
        remote)
          if [[ "$line[1]" = "sync" ]]; then
            _values 'sync remote commands' ls
          fi
          ;;
        backup)
          _values 'backup commands' create list restore
          ;;
        bulk)
          _values 'bulk commands' label close archive import
          ;;
        beads)
          _values 'beads commands' import export
          ;;
        hooks)
          _values 'hooks commands' install
          ;;
        completion)
          _values 'shell' bash zsh fish
          ;;
      esac
      ;;
  esac
}

_lk "$@"
`

const fishCompletionScript = `complete -c lk -f
complete -c lk -n '__fish_use_subcommand' -a 'new ls show edit close open archive delete unarchive restore comment label parent children dep export sync doctor fsck backup recover bulk beads workspace hooks quickstart completion help'
complete -c lk -n '__fish_seen_subcommand_from comment' -a 'add'
complete -c lk -n '__fish_seen_subcommand_from label' -a 'add rm'
complete -c lk -n '__fish_seen_subcommand_from parent' -a 'set clear'
complete -c lk -n '__fish_seen_subcommand_from dep' -a 'add rm ls'
complete -c lk -n '__fish_seen_subcommand_from sync' -a 'status remote fetch pull push'
complete -c lk -n '__fish_seen_subcommand_from remote' -a 'ls'
complete -c lk -n '__fish_seen_subcommand_from backup' -a 'create list restore'
complete -c lk -n '__fish_seen_subcommand_from bulk' -a 'label close archive import'
complete -c lk -n '__fish_seen_subcommand_from beads' -a 'import export'
complete -c lk -n '__fish_seen_subcommand_from hooks' -a 'install'
complete -c lk -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
`
