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
checking the pid's start time in `/proc`.

## Keys

| Key | Action |
| --- | --- |
| `j` / `k`, arrows | move down / up |
| `Enter` | switch to the session's tmux pane |
| `g` / `G` | first / last session |
| `ctrl+d` / `ctrl+u` | half page down / up |
| `r` | refresh |
| `q` | quit |

`Enter` finds the tmux pane whose process tree contains the session's
`claude` process. Inside tmux it switches the current client there; outside
tmux it attaches to that session. Sessions not running under tmux get a
status-bar notice instead.

## Configuration

Colours live in `$XDG_CONFIG_HOME/agent-sessions/config.toml` (usually
`~/.config/agent-sessions/config.toml`); a commented default file is written
on first run. Each UI element — `running`, `waiting`, `idle`, `dimmed`,
`bar`, `selected` — is a `[styles.*]` section accepting `fg`/`bg` (ANSI/256
number or `#rrggbb` hex) and `bold`/`faint`/`reverse` booleans. Omitted keys
keep their defaults, so a config can override just one colour:

```toml
[styles.running]
fg = "#af87ff"
```
