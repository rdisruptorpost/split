//go:build windows

package agent

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsSnapshotAndCommandLineIncludeCurrentProcess(t *testing.T) {
	processes, err := snapshotProcesses()
	if err != nil {
		t.Fatal(err)
	}
	pid := uint32(os.Getpid())
	found := false
	for _, process := range processes {
		if process.pid == pid {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("current process %d was not present in Toolhelp snapshot", pid)
	}
	if commandLine := processCommandLine(pid); !strings.Contains(commandLine, "agent.test") {
		t.Fatalf("unexpected current process command line: %q", commandLine)
	}
}
