package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
	"golang.org/x/term"
)

// commandLogPath returns the XDG state location of the command log.
func commandLogPath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "agent-sessions", "commands.log")
}

// execCmd runs an expanded command template with the terminal attached,
// teeing everything it prints to the command log.
func execCmd(tmpl string, vars map[string]string) tea.Cmd {
	return tea.Exec(&loggedCommand{line: expandCommand(tmpl, vars)},
		func(err error) tea.Msg { return execDoneMsg{err} })
}

// editorInputMsg carries text collected in the user's editor back to the
// command whose {text-input} placeholder was waiting for it.
type editorInputMsg struct {
	tmpl  string
	token string            // the exact {text-input...} placeholder being filled
	vars  map[string]string // expansion vars captured at keypress
	text  string
	err   error
}

// editorInputCmd collects a {text-input} value in the user's editor: it opens
// an empty scratch file named after the placeholder's label, hands the
// terminal over, and reports back whatever was saved. Output isn't logged —
// a full-screen editor's escape sequences are noise in the command log.
func editorInputCmd(editor, label, tmpl, token string, vars map[string]string) tea.Cmd {
	f, err := os.CreateTemp("", inputFileSlug(label)+"-*.md")
	if err != nil {
		return func() tea.Msg { return editorInputMsg{err: err} }
	}
	path := f.Name()
	f.Close()
	// Via sh, so a configured editor can carry arguments of its own.
	cmd := exec.Command("sh", "-c", editor+" "+shellQuote(path))
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return editorInputMsg{err: err}
		}
		data, err := os.ReadFile(path)
		return editorInputMsg{
			tmpl:  tmpl,
			token: token,
			vars:  vars,
			text:  strings.TrimSpace(string(data)),
			err:   err,
		}
	})
}

// inputFileSlug turns a {text-input} label into a scratch-file name, so the
// editor's status line says what is being asked for.
func inputFileSlug(label string) string {
	slug := strings.Trim(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r - 'A' + 'a'
		}
		return '-'
	}, label), "-")
	if slug == "" {
		slug = "input"
	}
	return "agent-sessions-" + slug
}

// loggedCommand is a tea.ExecCommand that gives `sh -c line` the real
// terminal for input — so the command is exactly as interactive as it
// would be from a shell — while routing its output through a pty that is
// mirrored to both the terminal and the command log.
type loggedCommand struct {
	line   string
	stdin  io.Reader
	stdout io.Writer
}

func (c *loggedCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *loggedCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *loggedCommand) SetStderr(io.Writer)   {}

func (c *loggedCommand) Run() error {
	var logf *os.File
	if path := commandLogPath(); path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			logf, _ = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		}
	}
	start := time.Now()
	if logf != nil {
		defer logf.Close()
		fmt.Fprintf(logf, "=== %s run: %s\n", start.Format(time.DateTime), c.line)
	}
	err := c.run(logf)
	if logf != nil {
		status := "exit 0"
		if err != nil {
			status = err.Error()
		}
		fmt.Fprintf(logf, "=== %s %s (%s)\n\n",
			time.Now().Format(time.DateTime), status, time.Since(start).Round(time.Millisecond))
	}
	return err
}

func (c *loggedCommand) run(logf *os.File) error {
	cmd := exec.Command("sh", "-c", c.line)
	tty, isTTY := c.stdin.(*os.File)
	ptmx, pts, err := pty.Open()
	if err != nil || !isTTY || !term.IsTerminal(int(tty.Fd())) {
		// No pty to capture through: run plainly, output unlogged.
		cmd.Stdin, cmd.Stdout, cmd.Stderr = c.stdin, c.stdout, os.Stderr
		return cmd.Run()
	}
	defer ptmx.Close()

	// Keep the capture pty the same size as the real terminal.
	pty.InheritSize(tty, ptmx)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			pty.InheritSize(tty, ptmx)
		}
	}()

	cmd.Stdin = tty // the real terminal: input is untouched by us
	cmd.Stdout = pts
	cmd.Stderr = pts
	if err := cmd.Start(); err != nil {
		pts.Close()
		return err
	}
	pts.Close() // the child holds its own copy now

	out := c.stdout
	if logf != nil {
		out = io.MultiWriter(c.stdout, logf)
	}
	copied := make(chan struct{})
	go func() {
		io.Copy(out, ptmx) // ends when the last pty holder exits
		close(copied)
	}()
	err = cmd.Wait()
	select {
	case <-copied:
	case <-time.After(500 * time.Millisecond):
		// A background child kept the pty open; don't hold the TUI hostage.
	}
	return err
}
