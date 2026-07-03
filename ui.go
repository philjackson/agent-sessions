package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const refreshEvery = 2 * time.Second

var (
	barStyle      = lipgloss.NewStyle().Reverse(true)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	stateStyles   = map[SessionState]lipgloss.Style{
		StateRunning: lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		StateWaiting: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")),
		StateIdle:    lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	}
)

type model struct {
	sessions []Session
	cursor   int
	offset   int
	width    int
	height   int
	status   string
	notice   string // shown instead of status until the next keypress
}

type sessionsLoadedMsg struct {
	sessions []Session
	err      error
}

type tickMsg struct{}

func loadCmd() tea.Msg {
	sessions, err := LoadSessions()
	return sessionsLoadedMsg{sessions: sessions, err: err}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(loadCmd, tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case sessionsLoadedMsg:
		if msg.err != nil {
			m.status = "Error: " + msg.err.Error()
			return m, nil
		}
		// Keep the cursor on the same session even if sort order changed.
		var selectedID string
		if m.cursor < len(m.sessions) {
			selectedID = m.sessions[m.cursor].ID
		}
		m.sessions = msg.sessions
		m.cursor = min(m.cursor, max(0, len(m.sessions)-1))
		counts := map[SessionState]int{}
		for i, s := range m.sessions {
			if s.ID == selectedID {
				m.cursor = i
			}
			if s.Live {
				counts[s.State]++
			}
		}
		m.status = fmt.Sprintf("%d sessions, %d running, %d waiting, %d idle",
			len(m.sessions), counts[StateRunning], counts[StateWaiting], counts[StateIdle])

	case tickMsg:
		return m, tea.Batch(loadCmd, tickCmd())

	case tea.KeyMsg:
		m.notice = ""
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			return m.gotoSession()
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.sessions)-1))
		case "k", "up":
			m.cursor = max(m.cursor-1, 0)
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			m.cursor = max(0, len(m.sessions)-1)
		case "ctrl+d", "pgdown":
			m.cursor = min(m.cursor+m.pageSize()/2, max(0, len(m.sessions)-1))
		case "ctrl+u", "pgup":
			m.cursor = max(m.cursor-m.pageSize()/2, 0)
		case "r":
			m.status = "Refreshing..."
			return m, loadCmd
		}
	}
	m.clampOffset()
	return m, nil
}

// gotoSession jumps to the tmux pane of the selected live session: switching
// the current client when inside tmux, attaching to it when outside.
func (m model) gotoSession() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.sessions) {
		return m, nil
	}
	s := m.sessions[m.cursor]
	if !s.Live || s.PID == 0 {
		m.notice = "Session has no running claude process."
		return m, nil
	}
	pane, ok := tmuxPaneFor(s.PID)
	if !ok {
		m.notice = "Session is not running in a tmux pane."
		return m, nil
	}
	if insideTmux() {
		if err := tmuxSwitchTo(pane); err != nil {
			m.notice = "tmux: " + err.Error()
		}
		return m, nil
	}
	if err := tmuxSelect(pane); err != nil {
		m.notice = "tmux: " + err.Error()
		return m, nil
	}
	return m, tea.ExecProcess(exec.Command("tmux", "attach-session", "-t", pane),
		func(error) tea.Msg { return loadCmd() })
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
	b.WriteString(barStyle.Render(pad("q:Quit  j:Down  k:Up  Enter:Switch  g/G:Top/Bottom  r:Refresh", m.width)))
	b.WriteString("\n")

	page := m.pageSize()
	for i := 0; i < page; i++ {
		idx := m.offset + i
		if idx < len(m.sessions) {
			line := m.renderRow(idx)
			if idx == m.cursor {
				line = selectedStyle.Render(pad(line, m.width))
			} else if style, ok := stateStyles[m.sessions[idx].State]; ok {
				line = style.Render(line)
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
	b.WriteString(barStyle.Render(pad(status, m.width)))
	return b.String()
}

func (m model) renderRow(idx int) string {
	s := m.sessions[idx]
	line := fmt.Sprintf("%4d %-7s  %s  %s  %s  %s",
		idx+1,
		string(s.State),
		s.Modified.Format("Jan 02 15:04"),
		truncPad(s.Project(), 28),
		truncPad(s.Branch, 24),
		s.Subject(),
	)
	return trunc(line, m.width)
}

func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func trunc(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w < 1 {
		return ""
	}
	return string(r[:w-1]) + "…"
}

func truncPad(s string, w int) string {
	return pad(trunc(s, w), w)
}
