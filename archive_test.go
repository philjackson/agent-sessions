package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T) (Session, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if _, err := gitOutput(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	os.WriteFile(filepath.Join(repo, "tracked"), []byte("initial"), 0600)
	git("add", ".")
	git("commit", "-m", "initial")
	wt := filepath.Join(root, "wt")
	git("worktree", "add", "-b", "feature", wt)
	project := filepath.Join(root, ".claude", "projects", "project")
	os.MkdirAll(filepath.Join(project, "session", "subagents"), 0700)
	file := filepath.Join(project, "session.jsonl")
	os.WriteFile(file, []byte(`{"type":"user","cwd":"`+wt+`","gitBranch":"feature","message":{"role":"user","content":"Find this conversation"}}`+"\n"), 0600)
	os.WriteFile(filepath.Join(project, "session", "subagents", "child.jsonl"), []byte("sidecar"), 0600)
	return Session{ID: "session", File: file, CWD: wt, Branch: "feature", Title: "Find this conversation"}, repo, wt
}

func TestArchiveCleanWorktreeAndSearch(t *testing.T) {
	s, repo, wt := archiveFixture(t)
	p, err := inspectArchive(s)
	if err != nil {
		t.Fatal(err)
	}
	if p.Pending != "" || p.Worktree != wt {
		t.Fatalf("unexpected plan: %+v", p)
	}
	if err := p.Archive(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree not removed: %v", err)
	}
	if _, err := os.Stat(s.File); !os.IsNotExist(err) {
		t.Fatalf("transcript not removed: %v", err)
	}
	if _, err := gitOutput(repo, "rev-parse", "--verify", "feature"); err != nil {
		t.Fatal("branch should be retained", err)
	}
	sessions, err := newLoader().Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || !sessions[0].Archived || sessions[0].Branch != "feature" {
		t.Fatalf("archive not loaded: %+v", sessions)
	}
	archived := sessions[0]
	if b, err := os.ReadFile(filepath.Join(strings.TrimSuffix(archived.File, ".jsonl"), "subagents", "child.jsonl")); err != nil || string(b) != "sidecar" {
		t.Fatalf("lost sidecar: %s %v", b, err)
	}
	m := model{all: sessions}
	m.applyFilter()
	if len(m.sessions) != 0 {
		t.Fatal("archive visible in active view")
	}
	m.archiveView = true
	m.query = "conversation"
	m.applyFilter()
	if len(m.sessions) != 1 {
		t.Fatal("archive search failed")
	}
	if err := archived.Delete(); err != nil {
		t.Fatal(err)
	}
	sessions, err = loadArchive()
	if err != nil || len(sessions) != 0 {
		t.Fatalf("deleted archive reappears: %v %v", sessions, err)
	}
}

func TestArchiveDirtyRequiresConfirmation(t *testing.T) {
	s, _, wt := archiveFixture(t)
	os.WriteFile(filepath.Join(wt, "untracked"), []byte("pending"), 0600)
	p, err := inspectArchive(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Pending, "uncommitted") {
		t.Fatalf("dirty checkout missed: %+v", p)
	}
	if err := p.Archive(false); err == nil {
		t.Fatal("discard without confirmation")
	}
	if _, err := os.Stat(s.File); err != nil {
		t.Fatal("original lost", err)
	}
	if err := p.Archive(true); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveUnmergedAndChangedCheckout(t *testing.T) {
	s, _, wt := archiveFixture(t)
	os.WriteFile(filepath.Join(wt, "tracked"), []byte("feature"), 0600)
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "feature"}} {
		if _, err := gitOutput(wt, args...); err != nil {
			t.Fatal(err)
		}
	}
	p, err := inspectArchive(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Pending, "1 unmerged commits") {
		t.Fatalf("unmerged commit missed: %+v", p)
	}
	os.WriteFile(filepath.Join(wt, "new"), []byte("new change"), 0600)
	if err := p.Archive(true); err == nil {
		t.Fatal("changed checkout should require new confirmation")
	}
	if _, err := os.Stat(s.File); err != nil {
		t.Fatal("original lost", err)
	}
}

func TestArchiveCancelAndLiveRefusal(t *testing.T) {
	s, _, _ := archiveFixture(t)
	p, err := inspectArchive(s)
	if err != nil {
		t.Fatal(err)
	}
	m := model{archiving: &p}
	updated, cmd := m.Update(key("n"))
	if cmd != nil || updated.(model).archiving != nil {
		t.Fatal("cancel failed")
	}
	if _, err := os.Stat(s.File); err != nil {
		t.Fatal("cancel removed transcript", err)
	}
	s.PID = 123
	if _, err := inspectArchive(s); err == nil {
		t.Fatal("live session accepted")
	}
}

func TestArchiveSharedCheckoutPreserved(t *testing.T) {
	s, repo, _ := archiveFixture(t)
	s.CWD = repo
	s.Branch = "main"
	os.WriteFile(filepath.Join(repo, "pending"), []byte("keep me"), 0600)
	p, err := inspectArchive(s)
	if err != nil {
		t.Fatal(err)
	}
	if p.Worktree != "" {
		t.Fatal("main checkout classified as worktree")
	}
	if err := p.Archive(true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(repo, "pending")); err != nil || string(b) != "keep me" {
		t.Fatal("main checkout changed", err)
	}
}

func TestArchiveIgnoredFilesAndUnknownBase(t *testing.T) {
	s, repo, wt := archiveFixture(t)
	os.WriteFile(filepath.Join(wt, ".gitignore"), []byte("secret\n"), 0600)
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "ignore file"}} {
		if _, err := gitOutput(wt, args...); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(wt, "secret"), []byte("ignored pending file"), 0600)
	p, err := inspectArchive(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Pending, "ignored files") {
		t.Fatalf("ignored file missed: %+v", p)
	}
	if _, err := gitOutput(repo, "branch", "-m", "main", "trunk"); err != nil {
		t.Fatal(err)
	}
	p, err = inspectArchive(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Pending, "merge status unknown") {
		t.Fatalf("unknown base treated as clean: %+v", p)
	}
}

func TestArchiveFailedCleanupKeepsOriginal(t *testing.T) {
	s, repo, wt := archiveFixture(t)
	if _, err := gitOutput(repo, "worktree", "lock", wt); err != nil {
		t.Fatal(err)
	}
	p, err := inspectArchive(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Archive(false); err == nil {
		t.Fatal("locked worktree removed")
	}
	if _, err := os.Stat(s.File); err != nil {
		t.Fatal("original lost", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("checkout lost", err)
	}
	sessions, err := loadArchive()
	if err != nil || len(sessions) != 0 {
		t.Fatalf("failed cleanup left archive: %v %v", sessions, err)
	}
}

func TestArchiveDefaultLocation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_DATA_HOME", "")
	got, err := archiveRoot()
	want := filepath.Join(root, ".local", "share", "agent-sessions", "archive")
	if err != nil || got != want {
		t.Fatalf("got %s %v, want %s", got, err, want)
	}
}

func TestArchiveRefusesWorktreeUsedByAnotherLiveSession(t *testing.T) {
	s, _, wt := archiveFixture(t)
	p, err := inspectArchive(s)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := claudeDir("sessions")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(registry, 0700); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	data, err := json.Marshal(registrySession{PID: pid, SessionID: "other", StartedAt: procStartTime(pid), Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registry, strconv.Itoa(pid)+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.File), "other.jsonl"), []byte(`{"type":"user","cwd":"`+wt+`","gitBranch":"feature"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.Archive(false); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("live worktree was not refused: %v", err)
	}
	if _, err := os.Stat(s.File); err != nil {
		t.Fatal("original lost", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("worktree lost", err)
	}
}

func TestArchiveRefusesDetachedWorktree(t *testing.T) {
	s, _, wt := archiveFixture(t)
	if _, err := gitOutput(wt, "checkout", "--detach"); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectArchive(s); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("detached worktree accepted: %v", err)
	}
}

func archivedFixture(t *testing.T) (Session, Session) {
	t.Helper()
	original, _, _ := archiveFixture(t)
	p, err := inspectArchive(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Archive(false); err != nil {
		t.Fatal(err)
	}
	sessions, err := loadArchive()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("load archive: %v %v", sessions, err)
	}
	return original, sessions[0]
}

func TestUnarchiveRestoresSessionAndSidecar(t *testing.T) {
	original, archived := archivedFixture(t)
	transcript, err := os.ReadFile(archived.File)
	if err != nil {
		t.Fatal(err)
	}
	if err := archived.Unarchive(); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(original.File)
	if err != nil || string(restored) != string(transcript) {
		t.Fatalf("transcript not restored: %s %v", restored, err)
	}
	sidecar := filepath.Join(strings.TrimSuffix(original.File, ".jsonl"), "subagents", "child.jsonl")
	if data, err := os.ReadFile(sidecar); err != nil || string(data) != "sidecar" {
		t.Fatalf("sidecar not restored: %s %v", data, err)
	}
	sessions, err := newLoader().Load()
	if err != nil || len(sessions) != 1 || sessions[0].Archived {
		t.Fatalf("not restored to active list: %+v %v", sessions, err)
	}
	archives, err := loadArchive()
	if err != nil || len(archives) != 0 {
		t.Fatalf("archive remains: %v %v", archives, err)
	}
	if _, err := os.Stat(original.CWD); !os.IsNotExist(err) {
		t.Fatalf("unarchive should not recreate checkout: %v", err)
	}
}

func TestUnarchiveRefusesExistingDestinations(t *testing.T) {
	for _, sidecar := range []bool{false, true} {
		t.Run(strconv.FormatBool(sidecar), func(t *testing.T) {
			original, archived := archivedFixture(t)
			dest := original.File
			if sidecar {
				dest = strings.TrimSuffix(dest, ".jsonl")
				if err := os.Mkdir(dest, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(dest, []byte("existing session"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := archived.Unarchive(); err == nil {
				t.Fatal("existing destination overwritten")
			}
			if !sidecar {
				if data, err := os.ReadFile(dest); err != nil || string(data) != "existing session" {
					t.Fatal("existing transcript changed", err)
				}
			}
			if _, err := os.Stat(archived.File); err != nil {
				t.Fatal("archive lost", err)
			}
		})
	}
}

func TestUnarchiveFailureKeepsArchive(t *testing.T) {
	original, archived := archivedFixture(t)
	// A malformed sidecar makes copying fail before publication.
	sourceSidecar := strings.TrimSuffix(archived.File, ".jsonl")
	if err := os.RemoveAll(sourceSidecar); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceSidecar, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := archived.Unarchive(); err == nil {
		t.Fatal("malformed sidecar accepted")
	}
	if _, err := os.Stat(original.File); !os.IsNotExist(err) {
		t.Fatalf("partial transcript published: %v", err)
	}
	if _, err := os.Stat(archived.File); err != nil {
		t.Fatal("archive lost", err)
	}
	if _, err := os.Stat(strings.TrimSuffix(original.File, ".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("partial sidecar published: %v", err)
	}
}

func TestUnarchiveKeyOnlyInArchiveView(t *testing.T) {
	original, archived := archivedFixture(t)
	active := model{sessions: []Session{original}}
	updated, cmd := active.Update(key("u"))
	if cmd != nil || updated.(model).archiveBusy {
		t.Fatal("active view tried to unarchive")
	}
	m := model{archiveView: true, sessions: []Session{archived}, loader: newLoader()}
	updated, cmd = m.Update(key("u"))
	if cmd == nil || !updated.(model).archiveBusy {
		t.Fatal("u did not start unarchive")
	}
	result := cmd().(unarchiveDoneMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	updated, cmd = updated.(model).Update(result)
	if updated.(model).archiveBusy || cmd == nil {
		t.Fatal("unarchive did not finish and refresh")
	}
}
