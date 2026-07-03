package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// insideTmux reports whether this process runs under a tmux client.
func insideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// tmuxPaneFor returns the id of the tmux pane that pid runs in, found by
// walking pid's ancestor chain until it hits a pane's root process.
func tmuxPaneFor(pid int) (string, bool) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid} #{pane_id}").Output()
	if err != nil {
		return "", false
	}
	panes := map[int]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		panePID, id, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(panePID); err == nil {
			panes[n] = id
		}
	}
	for p := pid; p > 1; p = parentPID(p) {
		if id, ok := panes[p]; ok {
			return id, true
		}
	}
	return "", false
}

// tmux runs a tmux command, discarding output.
func tmux(args ...string) error {
	return exec.Command("tmux", args...).Run()
}

// tmuxSelect makes pane the active pane of the active window of its session.
func tmuxSelect(pane string) error {
	if err := tmux("select-pane", "-t", pane); err != nil {
		return err
	}
	return tmux("select-window", "-t", pane)
}

// tmuxSwitchTo moves the current tmux client to pane (inside tmux only).
func tmuxSwitchTo(pane string) error {
	if err := tmuxSelect(pane); err != nil {
		return err
	}
	return tmux("switch-client", "-t", pane)
}
