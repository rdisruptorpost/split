//go:build windows

package terminal

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestPowerShellConPTYRoundTrip(t *testing.T) {
	shell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		shell, err = exec.LookPath("powershell.exe")
	}
	if err != nil {
		t.Skip("PowerShell is not available")
	}

	const marker = "__SPLIT_CONPTY_OK__"
	events := make(chan Event, 16)
	session, err := Start("smoke", Command{
		Name: shell,
		Args: []string{"-NoLogo", "-NoProfile", "-Command", "Write-Output '" + marker + "'"},
	}, 80, 24, events)
	if err != nil {
		t.Fatalf("start ConPTY session: %v", err)
	}
	defer session.Close()

	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		if strings.Contains(session.Render(), marker) {
			if err := session.Resize(100, 30); err != nil {
				t.Fatalf("resize ConPTY session: %v", err)
			}
			return
		}

		select {
		case <-events:
		case <-timeout.C:
			state, stateErr := session.State()
			t.Fatalf("marker not rendered before timeout (state=%v, err=%v)", state, stateErr)
		}
	}
}

func TestPowerShellConPTYInteractiveRoundTrip(t *testing.T) {
	shell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		shell, err = exec.LookPath("powershell.exe")
	}
	if err != nil {
		t.Skip("PowerShell is not available")
	}

	const marker = "__SPLIT_INTERACTIVE_OK__"
	events := make(chan Event, 64)
	session, err := Start("interactive", Command{Name: shell, Args: []string{"-NoLogo"}}, 80, 24, events)
	if err != nil {
		t.Fatalf("start interactive ConPTY session: %v", err)
	}
	defer session.Close()

	session.Paste("echo " + marker)
	session.SendKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		if strings.Contains(session.Render(), marker) {
			return
		}
		select {
		case <-events:
		case <-timeout.C:
			state, stateErr := session.State()
			t.Fatalf("interactive marker not rendered before timeout (state=%v, err=%v, content=%q)", state, stateErr, session.Render())
		}
	}
}
