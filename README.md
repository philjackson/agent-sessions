# agent-sessions

A Mutt-style TUI for browsing Claude Code sessions on this machine.

```
go build -o agent-sessions .
./agent-sessions
```

## What it shows

Every session transcript under `~/.claude/projects/`, newest first, one line
per session: index, state, last-modified time, project directory, git branch,
and a subject line (the session's AI-generated title, falling back to the
first typed prompt). The list auto-refreshes every 2 seconds.

Sessions with a running `claude` process show a state:

- `running` — Claude's turn is in progress
- `waiting` — blocked on the user, e.g. a permission prompt
- `idle` — waiting for the next prompt

State comes from `~/.claude/sessions/<pid>.json`, a registry each running
Claude Code instance maintains (status `busy`/`waiting`/`idle` plus the exact
session id). Registry files left behind by crashed processes are ignored by
checking that the pid is alive and started around the registry's `startedAt`
stamp; process inspection goes through gopsutil, so it works on both Linux
and macOS (the macOS path hasn't been smoke-tested yet).

## Keys

| Key | Action |
| --- | --- |
| `j` / `k`, arrows | move down / up |
| `Enter` | switch to the session's tmux pane |
| `/` | search: filter the list as you type, across all projects |
| `Esc` | clear the search limit |
| `g` / `G` | first / last session |
| `ctrl+d` / `ctrl+u` | half page down / up |
| `d` | delete the session after a y/n confirmation |
| `r` | refresh |
| `?` | help: list all keys and configured commands |
| `q` | quit |

`d` removes the session's transcript and sidecar directory from
`~/.claude/projects`, which only means `claude --resume` can no longer
offer that session — nothing a running Claude Code depends on. Sessions
with a live claude process are refused.

`/` matches case-insensitively against each session's title, project path,
branch, and session id. `Enter` keeps the match as a limit (shown in the
status bar) until `Esc` clears it.

`Enter` runs a configurable shell command (see below). The default finds the
tmux pane whose process tree contains the session's `claude` process and
jumps there: inside tmux it switches the current client, outside tmux it
attaches. Sessions the command's placeholders can't apply to (no live
process, not under tmux) get a status-bar notice instead.

## Configuration

Configuration lives in `$XDG_CONFIG_HOME/agent-sessions/config.toml`
(usually `~/.config/agent-sessions/config.toml`); a commented default file
is written on first run. Omitted keys keep their defaults.

Each UI element — `running`, `waiting`, `idle`, `dimmed`, `bar`, `selected`
— is a `[styles.*]` section accepting `fg`/`bg` (ANSI/256 number or
`#rrggbb` hex) and `bold`/`faint`/`reverse` booleans:

```toml
[styles.running]
fg = "#af87ff"
```

`[commands]` binds keys to shell commands run on the selected session. Any
Bubble Tea key name works — single characters, `enter`, or combos like
`"ctrl+x"` (quoted). `{id}`, `{pid}`, `{cwd}`, `{file}` and `{pane}` expand
to shell-quoted values ({pane} being the tmux pane hosting the session's
claude process), and the command gets the terminal while it runs, so
interactive commands work. Bindings take precedence over built-in keys;
set one to `""` to unbind it. `?` shows the active bindings.

`{project-picker}` is special: it first opens a selection screen listing
every known project (the distinct working directories across all sessions,
most recently used first) and expands to the chosen project's path.

```toml
[commands]
enter = "cd {cwd} && claude --resume {id}"
o = "cd {cwd} && $EDITOR ."
t = "less +G {file}"
n = "cd {project-picker} && claude"
```

## Tip: a tmux key that jumps to agent-sessions

To hop to the TUI from anywhere in tmux (starting it if it isn't running),
save this as an executable script, e.g. `~/.local/bin/agent-sessions-focus`:

```sh
#!/bin/sh
# Jump to the pane running agent-sessions, starting it if absent.
pane=$(tmux list-panes -a -F '#{pane_id} #{pane_current_command}' \
    | awk '$2 == "agent-sessions" {print $1; exit}')
if [ -n "$pane" ]; then
    tmux select-window -t "$pane"
    tmux select-pane -t "$pane"
    tmux switch-client -t "$pane"
else
    tmux new-window -n sessions agent-sessions
fi
```

and bind it in `~/.tmux.conf` (`prefix S`, or use `bind-key -n M-s` for a
prefix-less key):

```tmux
bind-key S run-shell ~/.local/bin/agent-sessions-focus
```

If two copies of the TUI are running, the script picks the first pane it
finds, and the match is on the binary name — adjust the awk pattern if you
install it under a different name.
