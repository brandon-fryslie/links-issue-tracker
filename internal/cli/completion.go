package cli

const bashCompletionScript = `# bash completion for lk
_lk_completions() {
  local current prev words cword
  _init_completion || return

  local commands="new ls show edit close open archive delete unarchive restore comment label parent children dep export sync doctor fsck backup recover bulk beads workspace quickstart completion help"
  local comment_subcommands="add"
  local label_subcommands="add rm"
  local parent_subcommands="set clear"
  local dep_subcommands="add rm ls"
  local sync_subcommands="export import status"
  local backup_subcommands="create list restore"
  local bulk_subcommands="label close archive import"
  local beads_subcommands="import export"

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
    'sync:sync export file'
    'doctor:database health check'
    'fsck:database integrity check and repair'
    'backup:snapshot management'
    'recover:restore from backup or sync file'
    'bulk:bulk issue operations'
    'beads:beads dolt import export'
    'workspace:show workspace info'
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
          _values 'sync commands' export import status
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
complete -c lk -n '__fish_use_subcommand' -a 'new ls show edit close open archive delete unarchive restore comment label parent children dep export sync doctor fsck backup recover bulk beads workspace quickstart completion help'
complete -c lk -n '__fish_seen_subcommand_from comment' -a 'add'
complete -c lk -n '__fish_seen_subcommand_from label' -a 'add rm'
complete -c lk -n '__fish_seen_subcommand_from parent' -a 'set clear'
complete -c lk -n '__fish_seen_subcommand_from dep' -a 'add rm ls'
complete -c lk -n '__fish_seen_subcommand_from sync' -a 'export import status'
complete -c lk -n '__fish_seen_subcommand_from backup' -a 'create list restore'
complete -c lk -n '__fish_seen_subcommand_from bulk' -a 'label close archive import'
complete -c lk -n '__fish_seen_subcommand_from beads' -a 'import export'
complete -c lk -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
`
