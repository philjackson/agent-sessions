package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const refreshEvery = 2 * time.Second

// Sessions with no live process and no activity for this long are dimmed.
const dimAfter = 24 * time.Hour

// Index column widths; the subject column takes the remaining width.
const (
	colProject = 28
	colBranch  = 24
)

// colState fits every state word the index can show.
var colState = func() int {
	w := len(StateUnknown)
	for _, st := range sessionStates {
		w = max(w, len(st))
	}
	return w
}()

// styles are the configured looks of each UI element.
type styles struct {
	bar      lipgloss.Style
	selected lipgloss.Style
	dim      lipgloss.Style
	state    map[SessionState]lipgloss.Style
}

func newStyles(cfg Config) styles {
	return styles{
		bar:      cfg.Styles.Bar.style(),
		selected: cfg.Styles.Selected.style(),
		dim:      cfg.Styles.Dimmed.style(),
		state: map[SessionState]lipgloss.Style{
			StateRunning: cfg.Styles.Running.style(),
			StateWaiting: cfg.Styles.Waiting.style(),
			StateIdle:    cfg.Styles.Idle.style(),
		},
	}
}

type model struct {
	loader    *loader
	styles    styles
	enterCmd  string    // command template bound to Enter
	all       []Session // every session, unfiltered
	sessions  []Session // what the index shows: all, limited by query
	query     string
	searching bool // the search prompt is open and capturing keys
	cursor    int
	offset    int
	width     int
	height    int
	loading   bool // a Load is in flight; don't start another
	status    string
	notice    string // shown instead of status until the next keypress
}

func newModel(cfg Config) model {
	return model{
		loader:   newLoader(),
		styles:   newStyles(cfg),
		enterCmd: cfg.Commands.Enter,
		loading:  true,
	}
}

type sessionsLoadedMsg struct {
	sessions []Session
	err      error
}

type tickMsg struct{}

type execDoneMsg struct{ err error }

func (m model) loadCmd() tea.Msg {
	sessions, err := m.loader.Load()
	return sessionsLoadedMsg{sessions: sessions, err: err}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadCmd, tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case sessionsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.status = "Error: " + msg.err.Error()
			return m, nil
		}
		m.all = msg.sessions
		m.applyFilter()

	case tickMsg:
		if m.loading {
			return m, tickCmd()
		}
		m.loading = true
		return m, tea.Batch(m.loadCmd, tickCmd())

	case execDoneMsg:
		if msg.err != nil {
			m.notice = "enter command: " + msg.err.Error()
		}
		if m.loading {
			return m, nil
		}
		m.loading = true
		return m, m.loadCmd

	case tea.KeyMsg:
		m.notice = ""
		if m.searching {
			m.handleSearchKey(msg)
			m.clampOffset()
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			return m.gotoSession()
		case "/":
			m.searching = true
			m.query = ""
			m.applyFilter()
		case "esc":
			if m.query != "" {
				m.query = ""
				m.applyFilter()
			}
		case "j", "down":
			m.cursor = min(m.cursor+1, m.lastRow())
		case "k", "up":
			m.cursor = max(m.cursor-1, 0)
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			m.cursor = m.lastRow()
		case "ctrl+d", "pgdown":
			m.cursor = min(m.cursor+m.pageSize()/2, m.lastRow())
		case "ctrl+u", "pgup":
			m.cursor = max(m.cursor-m.pageSize()/2, 0)
		case "r":
			if m.loading {
				break
			}
			m.loading = true
			m.status = "Refreshing..."
			return m, m.loadCmd
		}
	}
	m.clampOffset()
	return m, nil
}

// gotoSession runs the configured enter command for the selected session,
// handing it the terminal so interactive commands (tmux attach, editors)
// work. Commands using {pane} or {pid} need a live session; {pane} also
// needs the session's claude process to sit inside a tmux pane.
func (m model) gotoSession() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.sessions) {
		return m, nil
	}
	s := m.sessions[m.cursor]
	tmpl := m.enterCmd
	if tmpl == "" {
		m.notice = "No [commands] enter configured."
		return m, nil
	}
	vars := map[string]string{
		"id":   s.ID,
		"pid":  strconv.Itoa(s.PID),
		"cwd":  s.CWD,
		"file": s.File,
	}
	if strings.Contains(tmpl, "{pane}") || strings.Contains(tmpl, "{pid}") {
		if !s.Live() {
			m.notice = "Session has no running claude process."
			return m, nil
		}
	}
	if strings.Contains(tmpl, "{pane}") {
		pane, ok := tmuxPaneFor(s.PID)
		if !ok {
			m.notice = "Session is not running in a tmux pane."
			return m, nil
		}
		vars["pane"] = pane
	}
	cmd := exec.Command("sh", "-c", expandCommand(tmpl, vars))
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return execDoneMsg{err} })
}

// handleSearchKey edits the query while the search prompt is open. The list
// filters as the query changes; Enter keeps the limit, Esc clears it.
func (m *model) handleSearchKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "enter":
		m.searching = false
	case "esc":
		m.searching = false
		m.query = ""
		m.applyFilter()
	case "backspace":
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
			m.applyFilter()
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.query += string(msg.Runes)
			m.applyFilter()
		}
	}
}

// applyFilter rebuilds the visible list from the full one, keeps the cursor
// on the same session where possible, and refreshes the status counts.
func (m *model) applyFilter() {
	var selectedID string
	if m.cursor < len(m.sessions) {
		selectedID = m.sessions[m.cursor].ID
	}
	m.sessions = m.all
	if q := strings.ToLower(m.query); q != "" {
		m.sessions = nil
		for _, s := range m.all {
			if s.matches(q) {
				m.sessions = append(m.sessions, s)
			}
		}
	}
	m.cursor = min(m.cursor, m.lastRow())
	counts := map[SessionState]int{}
	for i, s := range m.sessions {
		if s.ID == selectedID {
			m.cursor = i
		}
		if s.Live() {
			counts[s.State]++
		}
	}
	noun := "sessions"
	if len(m.sessions) == 1 {
		noun = "session"
	}
	parts := []string{fmt.Sprintf("%d %s", len(m.sessions), noun)}
	for _, st := range sessionStates {
		parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
	}
	if m.query != "" {
		parts = append(parts, fmt.Sprintf("limit %q", m.query))
	}
	m.status = strings.Join(parts, ", ")
}

// lastRow is the highest valid cursor position.
func (m model) lastRow() int {
	return max(0, len(m.sessions)-1)
}

// pageSize is the number of index rows visible between the two bars.
func (m *model) pageSize() int {
	return max(1, m.height-2)
}

func (m *model) clampOffset() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.pageSize() {
		m.offset = m.cursor - m.pageSize() + 1
	}
}

func (m model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder
	b.WriteString(m.styles.bar.Render(pad("q:Quit  j:Down  k:Up  Enter:Switch  /:Search  Esc:Clear  g/G:Top/Bottom  r:Refresh", m.width)))
	b.WriteString("\n")

	page := m.pageSize()
	for i := 0; i < page; i++ {
		idx := m.offset + i
		if idx < len(m.sessions) {
			s := m.sessions[idx]
			line := m.renderRow(idx)
			switch {
			case idx == m.cursor:
				line = m.styles.selected.Render(pad(line, m.width))
			case s.Live():
				if style, ok := m.styles.state[s.State]; ok {
					line = style.Render(line)
				}
			case time.Since(s.Modified) > dimAfter:
				line = m.styles.dim.Render(line)
			}
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	pos := "all"
	if len(m.sessions) > page {
		pos = fmt.Sprintf("%d%%", (m.cursor+1)*100/len(m.sessions))
	}
	status := fmt.Sprintf("---Claude Sessions: %s---(%s)", m.status, pos)
	if m.notice != "" {
		status = m.notice
	}
	if m.searching {
		status = "Search: " + m.query + "█"
	}
	b.WriteString(m.styles.bar.Render(pad(status, m.width)))
	return b.String()
}

func (m model) renderRow(idx int) string {
	s := m.sessions[idx]
	line := fmt.Sprintf("%4d %-*s  %s  %s  %s  %s",
		idx+1,
		colState, string(s.State),
		s.Modified.Format("Jan 02 15:04"),
		truncPad(s.Project(), colProject),
		truncPad(s.Branch, colBranch),
		s.Subject(),
	)
	return trunc(line, m.width)
}

// pad and trunc both measure display cells (not runes or bytes), so wide
// characters in titles and paths can't skew the columns.
func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func trunc(s string, w int) string {
	return ansi.Truncate(s, w, "…")
}

func truncPad(s string, w int) string {
	return pad(trunc(s, w), w)
}
