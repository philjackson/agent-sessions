package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionMatchesTranscriptContent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(file, []byte(`{"type":"assistant","message":{"role":"assistant","content":"Fixed PR 75463 today"}}`+"\n"), 0600)
	fi, _ := os.Stat(file)
	s := Session{ID: "s", File: file, Size: fi.Size(), Modified: fi.ModTime(), Title: "Unrelated work"}

	ld := newLoader()
	if !s.matches("75463", ld) {
		t.Fatal("query not found in content")
	}
	if !s.matches("pr 75463", ld) { // case-insensitive, uses cache
		t.Fatal("case-insensitive content match failed")
	}
	if !s.matches("unrelated work", nil) { // metadata still matches, nil loader ok
		t.Fatal("metadata match failed")
	}
	if s.matches("never-there", ld) {
		t.Fatal("false positive")
	}
	if len(ld.texts) != 1 || ld.contentBytes == 0 {
		t.Fatalf("content not cached: %d %d", len(ld.texts), ld.contentBytes)
	}
}

func TestParseTranscriptDesktopTitles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(file, []byte(
		`{"type":"user","entrypoint":"claude-desktop","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nno project folder\n</system-reminder>"},{"type":"text","text":"What goals might leadership set?"}]}}`+"\n"+
			`{"type":"custom-title","customTitle":"Leadership goals"}`+"\n"+
			`{"type":"ai-title","aiTitle":"Something else"}`+"\n"), 0600)

	s := Session{File: file}
	parseTranscript(&s)
	if s.Title != "Leadership goals" {
		t.Fatalf("custom title should win, got %q", s.Title)
	}

	os.WriteFile(file, []byte(
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>x</system-reminder>"},{"type":"text","text":"What goals might leadership set?\nmore"}]}}`+"\n"), 0600)
	s = Session{File: file}
	parseTranscript(&s)
	if s.Title != "What goals might leadership set?" {
		t.Fatalf("prompt after injected block not used, got %q", s.Title)
	}
}

func TestAbbreviatePath(t *testing.T) {
	for in, want := range map[string]string{
		"~/Projects/One/Two": "~/Projects/O/Two",
		"~":                  "~",
		"~/Projects":         "~/Projects",
		"~/Projects/One":     "~/Projects/One",
		"/srv/app/web":       "/srv/a/web",
		"~/.config/Claude/x": "~/.config/C/x",
		"~/a/.hidden/Ünï/x":  "~/a/.h/Ü/x",
		"?":                  "?",
	} {
		if got := abbreviatePath(in); got != want {
			t.Errorf("abbreviatePath(%q) = %q, want %q", in, got, want)
		}
	}
}
