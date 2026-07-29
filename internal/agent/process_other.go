//go:build !windows

package agent

import "time"

func snapshotProcesses() ([]processInfo, error) {
	return nil, nil
}

func processCommandLine(uint32) string {
	return ""
}

func processStartTime(uint32) (time.Time, bool) {
	return time.Time{}, false
}
