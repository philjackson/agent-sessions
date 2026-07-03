package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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

// parentPID returns pid's parent, or 0 if unknown.
func parentPID(pid int) int {
	fields := procStatFields(pid)
	if len(fields) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(fields[1]) // stat field 4: ppid
	return n
}
