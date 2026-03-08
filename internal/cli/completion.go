package cli

const bashCompletionScript = `# bash completion for lk
_lk_completions() {
  local current prev words cword
  _init_completion || return

  local commands="new ls show edit close open archive delete comment label dep export sync beads workspace completion help"
  local comment_subcommands="add"
  local label_subcommands="add rm"
  local dep_subcommands="add rm"
  local sync_subcommands="export import status"
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
    dep)
      COMPREPLY=( $(compgen -W "${dep_subcommands}" -- "${current}") )
      return
      ;;
    sync)
      COMPREPLY=( $(compgen -W "${sync_subcommands}" -- "${current}") )
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
    'comment:add comment'
    'label:manage labels'
    'dep:manage dependencies'
    'export:write JSON export to stdout'
    'sync:sync export file'
    'beads:beads sqlite import export'
    'workspace:show workspace info'
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
        dep)
          _values 'dep commands' add rm
          ;;
        sync)
          _values 'sync commands' export import status
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
complete -c lk -n '__fish_use_subcommand' -a 'new ls show edit close open archive delete comment label dep export sync beads workspace completion help'
complete -c lk -n '__fish_seen_subcommand_from comment' -a 'add'
complete -c lk -n '__fish_seen_subcommand_from label' -a 'add rm'
complete -c lk -n '__fish_seen_subcommand_from dep' -a 'add rm'
complete -c lk -n '__fish_seen_subcommand_from sync' -a 'export import status'
complete -c lk -n '__fish_seen_subcommand_from beads' -a 'import export'
complete -c lk -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
`
