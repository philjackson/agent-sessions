package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SessionState describes what a live session is doing, as reported by the
// Claude Code process itself via ~/.claude/sessions/<pid>.json.
type SessionState string

const (
	StateRunning SessionState = "running" // Claude's turn is in progress
	StateWaiting SessionState = "waiting" // blocked on the user, e.g. a permission prompt
	StateIdle    SessionState = "idle"    // waiting for the next prompt
)

// Session is one Claude Code session transcript found on this machine.
type Session struct {
	ID       string
	File     string
	CWD      string
	Branch   string
	Slug     string
	Title    string
	Modified time.Time
	Size     int64
	Live     bool
	State    SessionState // empty unless Live
	PID      int          // the running claude process; 0 unless Live
}

// Project returns a short display name for the session's working directory.
func (s Session) Project() string {
	if s.CWD == "" {
		return "?"
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(s.CWD, home) {
		if s.CWD == home {
			return "~"
		}
		return "~" + strings.TrimPrefix(s.CWD, home)
	}
	return s.CWD
}

// Subject is the line shown in the index: AI title, else first prompt, else slug.
func (s Session) Subject() string {
	if s.Title != "" {
		return s.Title
	}
	if s.Slug != "" {
		return "(" + s.Slug + ")"
	}
	return "(empty session)"
}

// transcriptLine covers the JSONL fields we care about across entry types.
type transcriptLine struct {
	Type      string `json:"type"`
	AITitle   string `json:"aiTitle"`
	CWD       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	Slug      string `json:"slug"`
	IsMeta    bool   `json:"isMeta"`
	Message   *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

const (
	headScanBytes = 256 * 1024
	tailScanBytes = 64 * 1024
)

// LoadSessions scans ~/.claude/projects for session transcripts,
// newest first, with live processes marked.
func LoadSessions() ([]Session, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".claude", "projects")
	projectDirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var sessions []Session
	for _, pd := range projectDirs {
		if !pd.IsDir() {
			continue
		}
		dir := filepath.Join(root, pd.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			s := Session{
				ID:       strings.TrimSuffix(f.Name(), ".jsonl"),
				File:     filepath.Join(dir, f.Name()),
				Modified: info.ModTime(),
				Size:     info.Size(),
			}
			parseTranscript(&s)
			sessions = append(sessions, s)
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified.After(sessions[j].Modified)
	})
	markLive(sessions)
	return sessions, nil
}

// parseTranscript fills metadata by scanning the head of the file (for the
// first user prompt and title) and the tail (for the latest branch/cwd).
func parseTranscript(s *Session) {
	f, err := os.Open(s.File)
	if err != nil {
		return
	}
	defer f.Close()

	var firstPrompt string
	scan(io.LimitReader(f, headScanBytes), func(l transcriptLine) {
		absorb(s, l)
		if firstPrompt == "" {
			firstPrompt = userPrompt(l)
		}
	})

	if s.Size > headScanBytes {
		// Rescan the end of the file (overlapping the head scan is harmless:
		// absorb lets later lines win) so lastEv reflects the final entry.
		off := max(s.Size-tailScanBytes, 0)
		if _, err := f.Seek(off, io.SeekStart); err == nil {
			r := bufio.NewReader(f)
			r.ReadString('\n') // drop partial first line
			scan(r, func(l transcriptLine) { absorb(s, l) })
		}
	}

	if s.Title == "" {
		s.Title = firstPrompt
	}
}

// absorb copies metadata fields from a transcript line, later lines winning.
func absorb(s *Session, l transcriptLine) {
	if l.AITitle != "" {
		s.Title = l.AITitle
	}
	if l.CWD != "" {
		s.CWD = l.CWD
	}
	if l.GitBranch != "" {
		s.Branch = l.GitBranch
	}
	if l.Slug != "" {
		s.Slug = l.Slug
	}
}

// contentText returns the first text found in a message content value,
// which is either a plain string or an array of typed blocks.
func contentText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}

// scan parses JSONL lines from r, ignoring anything malformed or oversized.
func scan(r io.Reader, fn func(transcriptLine)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for sc.Scan() {
		var l transcriptLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		fn(l)
	}
}

// userPrompt extracts human-typed text from a user line, or "".
func userPrompt(l transcriptLine) string {
	if l.Type != "user" || l.IsMeta || l.Message == nil || l.Message.Role != "user" {
		return ""
	}
	text := strings.TrimSpace(contentText(l.Message.Content))
	// Skip slash-command wrappers, hook output, and injected reminders.
	if text == "" || strings.HasPrefix(text, "<") {
		return ""
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return text
}

// registrySession mirrors ~/.claude/sessions/<pid>.json, the per-process
// status file each running Claude Code instance maintains.
type registrySession struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	ProcStart  string `json:"procStart"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
}

// liveInfo is what the registry tells us about one running session.
type liveInfo struct {
	State SessionState
	PID   int
}

// markLive attaches the live state reported by running Claude Code processes.
func markLive(sessions []Session) {
	live := liveStates()
	for i := range sessions {
		if info, ok := live[sessions[i].ID]; ok {
			sessions[i].Live = true
			sessions[i].State = info.State
			sessions[i].PID = info.PID
		}
	}
}

// liveStates reads the session registry and returns sessionID -> liveInfo for
// sessions whose process is still alive. Registry files of crashed sessions
// can linger, so each pid is checked against its process start time.
func liveStates() map[string]liveInfo {
	live := map[string]liveInfo{}
	home, err := os.UserHomeDir()
	if err != nil {
		return live
	}
	dir := filepath.Join(home, ".claude", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return live
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var r registrySession
		if err := json.Unmarshal(data, &r); err != nil || r.SessionID == "" {
			continue
		}
		if r.ProcStart != procStartTime(r.PID) {
			continue // stale file: pid dead or recycled
		}
		state := SessionState(r.Status)
		if r.Status == "busy" {
			state = StateRunning
		}
		live[r.SessionID] = liveInfo{State: state, PID: r.PID}
	}
	return live
}

// procStatFields returns the fields of /proc/<pid>/stat that follow the
// parenthesized comm (which may contain spaces), or nil if pid is gone.
func procStatFields(pid int) []string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return nil
	}
	i := strings.LastIndexByte(string(data), ')')
	if i < 0 {
		return nil
	}
	return strings.Fields(string(data[i+1:]))
}

// procStartTime returns stat field 22 (process start time in clock ticks),
// or "" if the process doesn't exist.
func procStartTime(pid int) string {
	fields := procStatFields(pid)
	if len(fields) < 20 {
		return ""
	}
	return fields[19] // fields[0] is stat field 3 (state)
}
