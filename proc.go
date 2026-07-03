package main

import "github.com/shirou/gopsutil/v4/process"

// procStartTime returns the process start time in milliseconds since the
// epoch, or 0 if the process doesn't exist.
func procStartTime(pid int) int64 {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0
	}
	t, err := p.CreateTime()
	if err != nil {
		return 0
	}
	return t
}

// parentPID returns pid's parent, or 0 if unknown.
func parentPID(pid int) int {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0
	}
	ppid, err := p.Ppid()
	if err != nil {
		return 0
	}
	return int(ppid)
}
