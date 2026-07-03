package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
)

// defaultConfigTOML is both the shipped default configuration and the file
// written to the user's config directory on first run.
const defaultConfigTOML = `# agent-sessions configuration.
#
# Each style section accepts:
#   fg, bg                 colour: an ANSI/256 number ("0"-"255") or hex ("#rrggbb")
#   bold, faint, reverse   boolean attributes
# Omitted keys keep their defaults.

[styles.running]   # sessions whose turn is in progress
fg = "2"

[styles.waiting]   # sessions blocked on the user, e.g. a permission prompt
fg = "3"
bold = true

[styles.idle]      # sessions waiting for the next prompt
fg = "6"

[styles.dimmed]    # sessions with no activity for over a day
faint = true

[styles.bar]       # the help and status bars
reverse = true

[styles.selected]  # the cursor row
reverse = true

[commands]
# Shell commands bound to keys, run on the selected session. Keys are
# Bubble Tea key names: single characters ("o", "D"), "enter", or combos
# like "ctrl+x" and "f5" (quote names with + or . in them). {id}, {pid},
# {cwd}, {file} and {pane} expand to shell-quoted values; {pane} is the
# tmux pane hosting the session's claude process, and commands using it
# are only run for sessions found in a pane. Two placeholders are
# interactive and resolve before the command runs: {project-picker} opens
# a selection screen over every known project and expands to the chosen
# project's path, and {text-input} (or {text-input:Some label}) asks for
# a line of text. Bindings here take precedence over the built-in keys;
# set one to "" to unbind it.
#
# The default enter jumps to the session's pane: switch-client moves the
# client when run inside tmux, attach-session takes over the terminal
# when run outside it. More examples:
#   o = "cd {cwd} && $EDITOR ."
#   t = "less +G {file}"
#   n = "cd {project-picker} && claude"
#   c = 'tmux new-window -c {project-picker} "claude {text-input:Prompt}"'
enter = "tmux select-pane -t {pane} && tmux select-window -t {pane} && tmux switch-client -t {pane} 2>/dev/null || tmux attach-session -t {pane}"
`

// Config is the user-tunable configuration.
type Config struct {
	Styles struct {
		Running  StyleConfig `toml:"running"`
		Waiting  StyleConfig `toml:"waiting"`
		Idle     StyleConfig `toml:"idle"`
		Dimmed   StyleConfig `toml:"dimmed"`
		Bar      StyleConfig `toml:"bar"`
		Selected StyleConfig `toml:"selected"`
	} `toml:"styles"`
	Commands map[string]string `toml:"commands"`
}

// StyleConfig describes one visual element of the UI.
type StyleConfig struct {
	Fg      string `toml:"fg"`
	Bg      string `toml:"bg"`
	Bold    bool   `toml:"bold"`
	Faint   bool   `toml:"faint"`
	Reverse bool   `toml:"reverse"`
}

func (sc StyleConfig) style() lipgloss.Style {
	st := lipgloss.NewStyle()
	if sc.Fg != "" {
		st = st.Foreground(lipgloss.Color(sc.Fg))
	}
	if sc.Bg != "" {
		st = st.Background(lipgloss.Color(sc.Bg))
	}
	return st.Bold(sc.Bold).Faint(sc.Faint).Reverse(sc.Reverse)
}

// textInputRe matches {text-input} placeholders, with an optional prompt
// label after a colon: {text-input:Initial prompt}.
var textInputRe = regexp.MustCompile(`\{text-input(?::([^}]*))?\}`)

// expandCommand substitutes {key} placeholders in a command template with
// shell-quoted values.
func expandCommand(tmpl string, vars map[string]string) string {
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{"+k+"}", shellQuote(v))
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// configPath returns the XDG-style location of the config file.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-sessions", "config.toml"), nil
}

// loadConfig returns the defaults overlaid with the user's config file,
// writing a default file first if none exists yet.
func loadConfig() (Config, error) {
	var cfg Config
	if _, err := toml.Decode(defaultConfigTOML, &cfg); err != nil {
		return cfg, fmt.Errorf("built-in default config: %w", err)
	}
	path, err := configPath()
	if err != nil {
		return cfg, nil // no config dir on this system; run with defaults
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Best-effort: an unwritable config dir shouldn't stop the TUI.
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			os.WriteFile(path, []byte(defaultConfigTOML), 0o644)
		}
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	defaults := cfg.Commands
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	// Merge semantics for [commands], like the style sections: a user file
	// adding keys keeps the default bindings unless it redefines them.
	if cfg.Commands == nil {
		cfg.Commands = map[string]string{}
	}
	for k, v := range defaults {
		if _, ok := cfg.Commands[k]; !ok {
			cfg.Commands[k] = v
		}
	}
	return cfg, nil
}
