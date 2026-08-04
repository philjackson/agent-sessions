package main

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestTextInputEditor(t *testing.T) {
	cases := []struct {
		name              string
		mode, editor      string
		visual, editorEnv string
		want              string
	}{
		{name: "default is the in-app prompt", mode: "", visual: "nvim", want: ""},
		{name: "app mode ignores the environment", mode: "app", visual: "nvim", want: ""},
		{name: "configured editor wins", mode: "editor", editor: "emacs -nw", visual: "nvim", want: "emacs -nw"},
		{name: "VISUAL before EDITOR", mode: "editor", visual: "nvim", editorEnv: "vim", want: "nvim"},
		{name: "EDITOR when VISUAL is unset", mode: "editor", editorEnv: "vim", want: "vim"},
		{name: "blank config falls through", mode: "editor", editor: "  ", editorEnv: "vim", want: "vim"},
		{name: "nothing configured falls back to vi", mode: "editor", want: "vi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("VISUAL", c.visual)
			t.Setenv("EDITOR", c.editorEnv)
			var cfg Config
			cfg.Input.Mode, cfg.Input.Editor = c.mode, c.editor
			if got := cfg.textInputEditor(); got != c.want {
				t.Errorf("textInputEditor() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDefaultConfigUsesInAppInput(t *testing.T) {
	t.Setenv("VISUAL", "nvim")
	var cfg Config
	if _, err := toml.Decode(defaultConfigTOML, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.textInputEditor(); got != "" {
		t.Errorf("shipped default should use the in-app prompt, got editor %q", got)
	}
}

func TestInputFileSlug(t *testing.T) {
	cases := []struct{ label, want string }{
		{"Prompt", "agent-sessions-prompt"},
		{"Linear ticket", "agent-sessions-linear-ticket"},
		{"Input", "agent-sessions-input"},
		{"", "agent-sessions-input"},
		{"///", "agent-sessions-input"},
		{"Prompt (v2)!", "agent-sessions-prompt--v2"},
	}
	for _, c := range cases {
		if got := inputFileSlug(c.label); got != c.want {
			t.Errorf("inputFileSlug(%q) = %q, want %q", c.label, got, c.want)
		}
	}
}
