//go:build windows

package agent

import (
	"os"
	"strings"
	"testing"
	"time"
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
	startedAt, ok := processStartTime(pid)
	if !ok {
		t.Fatal("could not read current process start time")
	}
	now := time.Now()
	if startedAt.After(now.Add(time.Second)) {
		t.Fatalf("process start time %v is in the future", startedAt)
	}
	if now.Sub(startedAt) > 10*time.Minute {
		t.Fatalf("process start time %v is unexpectedly old", startedAt)
	}
}
