package main

import (
	_ "embed"
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
//
//go:embed config.default.toml
var defaultConfigTOML string

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
	CircleCI struct {
		Token    string            `toml:"token"`
		Projects map[string]string `toml:"projects"`
	} `toml:"circleci"`
}

// ciToken returns the configured CircleCI token, falling back to the
// conventional environment variables.
func (c Config) ciToken() string {
	if c.CircleCI.Token != "" {
		return c.CircleCI.Token
	}
	if t := os.Getenv("CIRCLECI_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("CIRCLE_TOKEN")
}

// ciOverrides returns the per-directory slug overrides with ~ expanded.
func (c Config) ciOverrides() map[string]string {
	out := map[string]string{}
	home, _ := os.UserHomeDir()
	for dir, slug := range c.CircleCI.Projects {
		if home != "" {
			if rest, ok := strings.CutPrefix(dir, "~"); ok {
				dir = home + rest
			}
		}
		out[dir] = slug
	}
	return out
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
