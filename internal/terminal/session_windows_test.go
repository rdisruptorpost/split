//go:build windows

package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
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

	const marker = "__SPLIT_INTERACTIVE_P_OK__"
	events := make(chan Event, 64)
	session, err := Start("interactive", Command{Name: shell, Args: []string{"-NoLogo"}}, 80, 24, events)
	if err != nil {
		t.Fatalf("start interactive ConPTY session: %v", err)
	}
	defer session.Close()

	session.Paste("echo __SPLIT_INTERACTIVE_")
	session.SendKey(tea.KeyPressMsg(tea.Key{
		Text: "P", Code: 'p', ShiftedCode: 'P', Mod: tea.ModShift,
	}))
	session.Paste("_OK__")
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

func TestDefaultPowerShellReportsCWDAndAcceptsPromptGatedCommand(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		if _, pwshErr := exec.LookPath("pwsh.exe"); pwshErr != nil {
			t.Skip("PowerShell is not available")
		}
	}

	root := t.TempDir()
	nested := filepath.Join(root, "nested directory")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 128)
	session, err := Start("cwd", DefaultShell(root), 80, 24, events)
	if err != nil {
		t.Fatalf("start default PowerShell: %v", err)
	}
	defer session.Close()

	waitFor := func(description string, condition func() bool) {
		t.Helper()
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		for !condition() {
			select {
			case <-events:
			case <-timer.C:
				state, stateErr := session.State()
				t.Fatalf("%s before timeout (cwd=%q, state=%v, err=%v, content=%q)",
					description, session.WorkingDirectory(), state, stateErr, session.Render())
			}
		}
	}
	waitFor("initial prompt did not report cwd", func() bool {
		return filepath.Clean(session.WorkingDirectory()) == filepath.Clean(root)
	})

	quotedNested := strings.ReplaceAll(nested, "'", "''")
	session.Paste("Set-Location -LiteralPath '" + quotedNested + "'")
	session.SendKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	waitFor("changed prompt did not report cwd", func() bool {
		return filepath.Clean(session.WorkingDirectory()) == filepath.Clean(nested)
	})

	const marker = "__SPLIT_PROMPT_READY_OK__"
	session.SendCommandWhenReady("Write-Output '" + marker + "'")
	waitFor("prompt-gated command did not run", func() bool {
		return strings.Contains(session.Render(), marker)
	})
}
