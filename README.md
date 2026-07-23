# agent-sessions

A Mutt-style TUI for browsing Claude Code sessions on this machine.

```
go build -o agent-sessions .
./agent-sessions
```

## What it shows

Every session transcript under `~/.claude/projects/`, most recently active
first, one line per session: index, state, last-modified time, project
directory, git branch, the tmux pane hosting the session (as
`session:window.pane`, for live sessions found in one), and a subject line
(the session's AI-generated title, falling back to the first typed prompt).
The list auto-refreshes every 2 seconds. The branch is whatever the
session's directory is on right now (read from `.git/HEAD`, worktrees
included), falling back to the transcript's last-recorded branch when the
directory no longer exists.

Live sessions (a running claude process) float to the top; the rest follow
by the timestamp of each transcript's last real entry, not the file's
modification time. Claude Code rewrites a transcript's mtime for content-free
changes too — a mode or permission-mode toggle, for instance — which would
otherwise float an untouched session to the top; keying off the last
timestamped entry keeps that from happening. Sessions with no timestamped
entries at all (bare mode-only stubs) sort to the bottom.

Each session's last assistant message — the thing Claude last said, e.g. the
`Done!` ending a turn — is shown too. By default it appears as an indented
detail line beneath the session, for the selected session and for the most
recently active ones, so recent answers stay on screen. See `[preview]` under
Configuration to change this to an inline column or turn it off.

Each row opens with a coloured status marker (a Nerd Font glyph by default),
so live sessions stand out at a glance:

- `running` — Claude's turn is in progress; an animated spinner in a bright
  colour
- `waiting` — blocked on the user, e.g. a permission prompt
- `idle` — waiting for the next prompt
- **unread** — a session that finished a turn (went `running` → `idle`) while
  you were watching but that you haven't opened yet, in a bright "attention"
  colour (orange by default). This is what tells apart the session that *just*
  said `Done!` from every other long-idle one. The marker clears when you open
  the session with `Enter`. It's tracked in-memory, so it only covers turns
  that finish while agent-sessions is running, and resets when you quit.

The markers and their colours are configurable — Nerd Font icons, emoji, or
plain dots — see `[status]` and the `[styles.*]` sections under Configuration.

Live sessions running inside a tmux pane — the ones the default `Enter`
command can jump to — are additionally marked with a `⊟` glyph (configurable
via `[tmux]`), so you can see which are attachable without pressing `Enter`.

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
| `Enter` | jump to a live session's tmux pane; resume a dead session |
| `/` | search: filter the list as you type, across all projects |
| `f` | filter menu: `p` filters by project, `b` by branch (chosen via the picker) |
| `Esc` | clear the search and project/branch filters |
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
branch, and session id. `f` opens the filter menu: the top bar lists the
follow-up keys, `p` filters the list to a single project and `b` to a
single branch, each chosen via the picker (when a project filter is active,
the branch picker offers only that project's branches). `Esc` — or any
unbound key — cancels the menu. The filters are shown in the status bar,
combine with each other, and stay until `Esc` clears them.

The picker is fzf-style: just start typing to narrow the list (a
subsequence match, so `agt` finds `agent-sessions`), move with the arrows
or `ctrl+j`/`ctrl+k`, pick with `Enter`, cancel with `Esc`. It is the
default selection UI, used anywhere a choice is asked for.

`Enter` runs a configurable shell command (see below). For a live session
the default finds the tmux pane whose process tree contains the session's
`claude` process and jumps there: inside tmux it switches the current
client, outside tmux it attaches. For a dead session it resumes the
conversation with `claude --resume` in a fresh tmux window.

## Configuration

Configuration lives in `$XDG_CONFIG_HOME/agent-sessions/config.toml`
(usually `~/.config/agent-sessions/config.toml`); on first run the shipped
default file — [`config.default.toml`](config.default.toml), embedded in
the binary at build time — is written there. Omitted keys keep their
defaults.

Each UI element — `running`, `waiting`, `idle`, `unread`, `offline`,
`dimmed`, `bar`, `selected`, `preview` — is a `[styles.*]` section accepting
`fg`/`bg` (ANSI/256 number or `#rrggbb` hex) and `bold`/`faint`/`reverse`
booleans:

```toml
[styles.running]
fg = "#af87ff"
```

`[circleci]` enables a CI column showing the latest CircleCI status of each
session's branch (`pass`, `fail`, `run`, `hold`, or `-` for no pipelines).
Set `token` (or export `$CIRCLECI_TOKEN`/`$CIRCLE_TOKEN`); without a token
the column is hidden. Statuses are fetched in the background for visible
rows and cached for 30 seconds. The CircleCI project slug is derived from
each project's git origin remote (`github.com/org/repo` → `gh/org/repo`);
override it per directory when needed:

```toml
[circleci]
token = ""
[circleci.projects]
"~/Projects/foo" = "gh/acme/foo"
```

The `[status]` section sets the per-status marker glyphs. Defaults are Nerd
Font icons; swap them for plain dots or emoji if your terminal lacks a Nerd
Font. `running = "spinner"` animates a braille spinner instead of a static
glyph:

```toml
[status]
running = "spinner"    # or a glyph, e.g. "●" / "🟢"
waiting = "●"          # "🟡" or "󰭙"
idle    = "·"          # "⚪"
unread  = "●"          # "🟠" — shown in the [styles.unread] colour
offline = " "          # non-live sessions
words   = true         # set false for a compact, icon-only column
```

The `[preview]` section controls the last-message display:

```toml
[preview]
mode = "row"      # "row" (detail line beneath), "column" (inline), or "off"
recent = 5        # in row mode, always preview this many recent sessions...
within = "20m"    # ...that were modified within this window (a Go duration)
```

The selected session is always previewed. `recent`/`within` only apply in
`row` mode; `column` mode shows every session's message inline (capping the
subject to make room), and `off` hides it.

The `[tmux]` section sets the marker shown on tmux-attachable sessions:

```toml
[tmux]
glyph = "⊟"   # set to "" to hide the marker
```

`[commands]` binds keys to shell commands run on the selected session. Any
Bubble Tea key name works — single characters, `enter`, or combos like
`"ctrl+x"` (quoted). The command gets the terminal while it runs, so
interactive commands work. Bindings take precedence over built-in keys;
set one to `""` to unbind it. `?` shows the active bindings.

### Command placeholders

Every placeholder expands to a shell-quoted value, so paths and typed text
survive word-splitting.

| Placeholder | Expands to |
| --- | --- |
| `{id}` | the session id (as used by `claude --resume`) |
| `{cwd}` | the session's working directory |
| `{file}` | the path of the session's transcript (`.jsonl`) |
| `{state}` | `running`/`waiting`/`idle` for live sessions, empty otherwise |
| `{pid}` | the pid of the session's running `claude` process |
| `{pane}` | the tmux pane hosting the session's `claude` process |
| `{ci-build-url}` | the latest CircleCI build's page (needs `[circleci]`) |
| `{project-picker}` | interactive: the project chosen from a selection screen |
| `{text-input}` | interactive: a line of text typed into the status bar |

`{pid}` and `{pane}` only apply to live sessions, and `{pane}` further
requires the process to sit inside a tmux pane — commands using them show a
status-bar notice instead of running when that doesn't hold. Likewise
`{ci-build-url}` needs the session's project to have a known CircleCI slug;
it deep-links to the latest fetched workflow, falling back to the branch's
pipelines page (e.g. `b = "xdg-open {ci-build-url}"`). Appending `?`
makes them **optional**: `{pane?}` and `{pid?}` expand to an empty string
instead, so a single command can branch. That's how the default `enter`
jumps to a live session's pane but resumes a dead one:

```toml
enter = '''
if [ -n {pane?} ]; then
    tmux select-pane -t {pane?} && tmux select-window -t {pane?}
    tmux switch-client -t {pane?} 2>/dev/null || tmux attach-session -t {pane?}
else
    p=$(tmux new-window -P -F "#{pane_id}" -c {cwd})
    tmux send-keys -t "$p" "claude --resume {id}" Enter
fi
'''
```

For anything more elaborate, hand the facts to a script and decide there:
`enter = "open-session {state} {pane?} {cwd} {id}"`.

The two interactive placeholders resolve one after another before the
command runs, and compose freely with the rest. `{project-picker}` lists
every known project (the distinct working directories across all sessions,
most recently used first) in the type-to-narrow picker described above. `{text-input}` accepts an optional label after a
colon that names the prompt: `{text-input:Prompt}`. `Esc` at any step
cancels the whole command.

```toml
[commands]
enter = "cd {cwd} && claude --resume {id}"
o = "cd {cwd} && $EDITOR ."
t = "less +G {file}"
n = "cd {project-picker} && claude"
```

### Command log

Every command run from a binding is appended, with timestamps, exit status,
duration, and everything it printed, to
`$XDG_STATE_HOME/agent-sessions/commands.log` (usually
`~/.local/state/agent-sessions/commands.log`). When a command fails, the
status-bar notice points there — that's the first place to look when a
binding misbehaves. Commands run with the real terminal as their input, so
they stay fully interactive; only their output is captured.

## Helpers

The [`helpers/`](helpers/) directory holds ready-made scripts to copy
somewhere on your `PATH` (e.g. `~/.local/bin`) and adapt:

- [`agent-sessions-focus`](helpers/agent-sessions-focus) — jump to the
  tmux pane running the TUI from anywhere, starting it in a new window if
  it isn't running. Made to hang off a tmux key binding; see the tip below.
- [`new-claude`](helpers/new-claude) — a worked example of pushing command
  logic into a script: given a project directory and an opening prompt
  (e.g. `C = "new-claude {project-picker} {text-input:Prompt}"`), it
  switches to a worktrunk (`wt`) worktree — offering to create one, and
  reusing the branch if it already exists — then starts `claude` with the
  prompt in a fresh tmux window via an interactive shell, so version
  managers and per-directory environments apply.
- [`linear-claude`](helpers/linear-claude) — the same worktree flow driven
  by a Linear ticket (e.g.
  `L = "linear-claude {project-picker} {text-input:Linear ticket}"`): it
  fetches the ticket with the [linear
  CLI](https://github.com/schpet/linear-cli) (authenticated via
  `linear auth login`; also needs `jq`), names the worktree branch after
  Linear's suggested branch name for the issue, and starts `claude`
  prompted with the ticket's title, description, and URL to implement it.

## Tip: create a new Claude session from the TUI

The shipped default binds `c` to pick a project, ask for the opening
prompt, and start `claude` with it in a fresh tmux window:

```toml
[commands]
c = '''
p=$(tmux new-window -P -F "#{pane_id}" -c {project-picker})
tmux send-keys -t "$p" "claude {text-input:Prompt}" Enter
'''
```

Note the shape: the window is opened with *no* command — so it starts your
normal interactive shell, applying whatever environment setup you use
(rc files, version managers such as asdf, per-directory environments such
as direnv) — and the claude invocation is then typed into it with
`send-keys`. The simpler `tmux new-window -c {project-picker} "claude ..."`
would run claude via a non-interactive shell where none of that setup
applies. The default `enter` uses the same pattern for its dead-session
branch. Use `split-window` instead of `new-window` for a pane in the
current window.

Quoting subtlety: the expanded `{text-input:...}` value is single-quote
escaped, and the double-quote wrapper hands it intact to the window's
shell — a prompt containing a literal `"` is the one thing it can't carry.

## Tip: a tmux key that jumps to agent-sessions

To hop to the TUI from anywhere in tmux (starting it if it isn't running),
copy [`helpers/agent-sessions-focus`](helpers/agent-sessions-focus) to
somewhere on your `PATH`, e.g. `~/.local/bin`, and bind it in
`~/.tmux.conf` (`prefix S`, or use `bind-key -n M-s` for a prefix-less
key):

```tmux
bind-key S run-shell ~/.local/bin/agent-sessions-focus
```

If two copies of the TUI are running, the script picks the first pane it
finds, and the match is on the binary name — adjust the awk pattern if you
install it under a different name.
