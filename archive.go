package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func archiveRoot() (string, error) {
	root := os.Getenv("XDG_DATA_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(root, "agent-sessions", "archive"), nil
}

type archivePlan struct {
	Session  Session
	Worktree string
	Repo     string
	Pending  string
	Revision string
}

func gitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Inspect before offering destructive cleanup. A missing checkout is normal for
// old sessions, but other inspection failures must never be treated as clean.
func inspectArchive(s Session) (archivePlan, error) {
	p := archivePlan{Session: s}
	if s.Archived {
		return p, errors.New("session is already archived")
	}
	if s.Live() {
		return p, errors.New("session has a running claude process")
	}
	if _, ok := liveStates()[s.ID]; ok {
		return p, errors.New("session has a running claude process")
	}
	if s.CWD == "" {
		return p, nil
	}
	if _, err := os.Stat(s.CWD); errors.Is(err, os.ErrNotExist) {
		return p, nil
	} else if err != nil {
		return p, err
	}
	// No .git in this directory or an ancestor means this is not a checkout.
	dir := s.CWD
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return p, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p, nil
		}
		dir = parent
	}
	top, err := gitOutput(s.CWD, "rev-parse", "--show-toplevel")
	if err != nil {
		return p, err
	}
	common, err := gitOutput(top, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return p, err
	}
	gitdir, err := gitOutput(top, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return p, err
	}
	if common != gitdir {
		p.Worktree = top
		// The common git directory also works for bare repositories.
		p.Repo = common
	}
	branch, err := gitOutput(top, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		branch = "HEAD"
	}
	if branch == "HEAD" && p.Worktree != "" {
		return p, errors.New("create a branch before archiving a detached worktree so its commits remain reachable")
	}
	p.Session.Branch = branch
	p.Revision, err = gitOutput(top, "rev-parse", "HEAD")
	if err != nil {
		return p, err
	}
	dirty, err := gitOutput(top, "status", "--porcelain", "--untracked-files=normal", "--ignored=matching")
	if err != nil {
		return p, err
	}
	var pending []string
	if dirty != "" {
		pending = append(pending, "uncommitted/untracked/ignored files")
	}
	// Compare with the default branch, rather than only the tracking branch:
	// pushed feature commits may still not have been merged.
	base, _ := gitOutput(top, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	candidates := []string{base, "refs/heads/main", "refs/heads/master"}
	found := false
	for _, ref := range candidates {
		if ref == "" {
			continue
		}
		if _, err := gitOutput(top, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
			continue
		}
		count, err := gitOutput(top, "rev-list", "--count", ref+"..HEAD")
		if err != nil {
			return p, err
		}
		n, err := strconv.Atoi(count)
		if err != nil {
			return p, err
		}
		if n > 0 {
			pending = append(pending, fmt.Sprintf("%d unmerged commits (branch retained)", n))
		}
		found = true
		break
	}
	if !found {
		pending = append(pending, "merge status unknown (no default branch)")
	}
	p.Pending = strings.Join(pending, "; ")
	return p, nil
}

// Archive copies everything first, then removes the checkout and original.
// Branch refs are deliberately retained, including unmerged commits.
func (p archivePlan) Archive(discard bool) error {
	fresh, err := inspectArchive(p.Session)
	if err != nil {
		return err
	}
	if fresh.Worktree != p.Worktree || fresh.Session.Branch != p.Session.Branch || fresh.Revision != p.Revision || fresh.Pending != p.Pending {
		return errors.New("checkout changed; retry archiving")
	}
	if fresh.Pending != "" && !discard {
		return errors.New("pending changes require confirmation; retry archiving")
	}
	if p.Worktree != "" {
		sessions, err := newLoader().Load()
		if err != nil {
			return err
		}
		for _, s := range sessions {
			if s.Live() && (s.CWD == p.Worktree || strings.HasPrefix(s.CWD, p.Worktree+string(os.PathSeparator))) {
				return errors.New("worktree is in use by a running claude session")
			}
		}
	}
	root, err := archiveRoot()
	if err != nil {
		return err
	}
	parent := filepath.Join(root, filepath.Base(filepath.Dir(p.Session.File)))
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	dest := filepath.Join(parent, p.Session.ID)
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		return errors.New("archive destination already exists or is inaccessible")
	}
	stage, err := os.MkdirTemp(parent, ".archive-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	transcript, err := os.ReadFile(p.Session.File)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, p.Session.ID+".jsonl"), transcript, 0600); err != nil {
		return err
	}
	sidecar := strings.TrimSuffix(p.Session.File, ".jsonl")
	if _, err := os.Stat(sidecar); err == nil {
		if err := os.CopyFS(filepath.Join(stage, p.Session.ID), os.DirFS(sidecar)); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	metadata, err := json.Marshal(fresh.Session)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "metadata.json"), metadata, 0600); err != nil {
		return err
	}
	if err := os.Rename(stage, dest); err != nil {
		return err
	}
	if p.Worktree != "" {
		args := []string{"worktree", "remove"}
		if discard {
			args = append(args, "--force")
		}
		args = append(args, "--", p.Worktree)
		if _, err := gitOutput(p.Repo, args...); err != nil {
			os.RemoveAll(dest)
			return err
		}
	}
	return p.Session.Delete()
}

func loadArchive() ([]Session, error) {
	root, err := archiveRoot()
	if err != nil {
		return nil, err
	}
	projects, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, project.Name()))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".archive-") {
				continue
			}
			dir := filepath.Join(root, project.Name(), entry.Name())
			data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
			if err != nil {
				return nil, err
			}
			var s Session
			if err := json.Unmarshal(data, &s); err != nil {
				return nil, fmt.Errorf("archive %s: %w", dir, err)
			}
			if s.ID != entry.Name() {
				return nil, fmt.Errorf("archive %s: session ID mismatch", dir)
			}
			s.File = filepath.Join(dir, s.ID+".jsonl")
			s.Archived, s.PID, s.State, s.Pane, s.Worktree = true, 0, "", "", false
			sessions = append(sessions, s)
		}
	}
	return sessions, nil
}

// Unarchive restores a conversation to the current user's Claude projects
// directory. Existing transcripts and sidecars are never overwritten.
// Checkout files discarded during archiving cannot be restored here.
func (s Session) Unarchive() error {
	if !s.Archived {
		return errors.New("session is not archived")
	}
	root, err := archiveRoot()
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.File)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) != 2 || parts[0] == ".." || parts[1] != s.ID || s.ID == "." || s.ID == ".." || filepath.Base(s.File) != s.ID+".jsonl" {
		return errors.New("invalid archived session path")
	}
	projects, err := claudeDir("projects")
	if err != nil {
		return err
	}
	project := filepath.Join(projects, parts[0])
	if err := os.MkdirAll(project, 0700); err != nil {
		return err
	}
	dest := filepath.Join(project, s.ID+".jsonl")
	sidecar := filepath.Join(project, s.ID)
	for _, path := range []string{dest, sidecar} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("restore destination already exists or is inaccessible: %s", path)
		}
	}
	stage, err := os.MkdirTemp(project, ".unarchive-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	data, err := os.ReadFile(s.File)
	if err != nil {
		return err
	}
	stagedFile := filepath.Join(stage, s.ID+".jsonl")
	if err := os.WriteFile(stagedFile, data, 0600); err != nil {
		return err
	}
	// Preserve the transcript timestamp so restoring does not reorder stubs.
	if info, err := os.Stat(s.File); err == nil {
		if err := os.Chtimes(stagedFile, info.ModTime(), info.ModTime()); err != nil {
			return err
		}
	}
	sourceSidecar := strings.TrimSuffix(s.File, ".jsonl")
	hasSidecar := false
	if _, err := os.Stat(sourceSidecar); err == nil {
		if err := os.CopyFS(filepath.Join(stage, s.ID), os.DirFS(sourceSidecar)); err != nil {
			return err
		}
		hasSidecar = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hasSidecar {
		// Mkdir reserves the destination exclusively, including against empty
		// directories or symlinks created after the initial collision check.
		if err := os.Mkdir(sidecar, 0700); err != nil {
			return err
		}
		if err := os.CopyFS(sidecar, os.DirFS(filepath.Join(stage, s.ID))); err != nil {
			os.RemoveAll(sidecar)
			return err
		}
	}
	// Publish a complete transcript atomically without replacing an existing file.
	if err := os.Link(stagedFile, dest); err != nil {
		if hasSidecar {
			os.RemoveAll(sidecar)
		}
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("session restored, but archive cleanup failed: %w", err)
	}
	return nil
}
