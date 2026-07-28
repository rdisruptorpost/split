//go:build !windows

package agent

func snapshotProcesses() ([]processInfo, error) {
	return nil, nil
}

func processCommandLine(uint32) string {
	return ""
}
