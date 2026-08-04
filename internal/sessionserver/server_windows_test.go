//go:build windows

package sessionserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"split/internal/state"
)

func TestRuntimeKeepsTerminalAliveAcrossClientDetach(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	endpoint, err := Endpoint(statePath)
	if err != nil {
		t.Fatal(err)
	}

	serverResult := make(chan error, 1)
	go func() { serverResult <- Run(root, statePath) }()
	t.Cleanup(func() { _ = Stop(statePath) })

	first := dialTestRuntime(t, endpoint)
	firstEncoder := json.NewEncoder(first)
	firstDecoder := json.NewDecoder(first)
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestAttach})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Version == protocolVersion })

	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestResize, Width: 100, Height: 30})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Content != "" })
	enter := tea.Key{Code: tea.KeyEnter}
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Content != "" })

	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestPaste, Paste: "echo SPLIT_KEEPALIVE"})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Content != "" })
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	readTestFrame(t, first, firstDecoder, func(value frame) bool {
		return strings.Contains(value.Content, "SPLIT_KEEPALIVE")
	})

	prefix := tea.Key{Code: 'z', Mod: tea.ModCtrl}
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &prefix})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Content != "" })
	quit := tea.Key{Code: 'q', Text: "q"}
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &quit})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Detach })
	_ = first.Close()

	second := dialTestRuntime(t, endpoint)
	defer second.Close()
	secondEncoder := json.NewEncoder(second)
	secondDecoder := json.NewDecoder(second)
	sendTestRequest(t, secondEncoder, request{Version: protocolVersion, Kind: requestAttach})
	readTestFrame(t, second, secondDecoder, func(value frame) bool {
		return strings.Contains(value.Content, "SPLIT_KEEPALIVE")
	})

	if err := Stop(statePath); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not stop after explicit shutdown")
	}
}

func TestRuntimePersistsPowerShellDirectoryAcrossExplicitRestart(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested workspace")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "state.db")
	endpoint, err := Endpoint(statePath)
	if err != nil {
		t.Fatal(err)
	}

	startRuntime := func() chan error {
		result := make(chan error, 1)
		go func() { result <- Run(root, statePath) }()
		return result
	}
	stopRuntime := func(result chan error) {
		t.Helper()
		if err := Stop(statePath); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runtime did not stop")
		}
	}
	t.Cleanup(func() { _ = Stop(statePath) })

	firstResult := startRuntime()
	first := dialTestRuntime(t, endpoint)
	firstEncoder := json.NewEncoder(first)
	firstDecoder := json.NewDecoder(first)
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestAttach})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Version == protocolVersion })
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestResize, Width: 160, Height: 36})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Content != "" })
	enter := tea.Key{Code: tea.KeyEnter}
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Content != "" })

	quotedNested := strings.ReplaceAll(nested, "'", "''")
	const changedMarker = "__SPLIT_CHANGED_CWD__"
	command := "Set-Location -LiteralPath '" + quotedNested +
		"'; Write-Output ('" + changedMarker + "' + (Get-Location).Path)"
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestPaste, Paste: command})
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	readTestFrame(t, first, firstDecoder, func(value frame) bool {
		plain := ansi.Strip(value.Content)
		return strings.Count(plain, changedMarker) >= 2 && strings.Contains(plain, "nested workspace")
	})

	// This verifies the detached runtime checkpoint, rather than relying only
	// on the clean-stop snapshot below.
	deadline := time.Now().Add(5 * time.Second)
	for {
		store, err := state.Open(statePath)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, loadErr := store.Load()
		_ = store.Close()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if len(snapshot.Projects) == 1 && len(snapshot.Projects[0].Panes) == 1 &&
			filepath.Clean(snapshot.Projects[0].Panes[0].WorkingDirectory) == filepath.Clean(nested) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime did not checkpoint cwd %q: %#v", nested, snapshot)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = first.Close()
	stopRuntime(firstResult)

	secondResult := startRuntime()
	second := dialTestRuntime(t, endpoint)
	secondEncoder := json.NewEncoder(second)
	secondDecoder := json.NewDecoder(second)
	sendTestRequest(t, secondEncoder, request{Version: protocolVersion, Kind: requestAttach})
	readTestFrame(t, second, secondDecoder, func(value frame) bool { return value.Version == protocolVersion })
	sendTestRequest(t, secondEncoder, request{Version: protocolVersion, Kind: requestResize, Width: 160, Height: 36})
	readTestFrame(t, second, secondDecoder, func(value frame) bool { return value.Content != "" })
	sendTestRequest(t, secondEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	readTestFrame(t, second, secondDecoder, func(value frame) bool { return value.Content != "" })
	const restoredMarker = "__SPLIT_RESTORED_CWD__"
	sendTestRequest(t, secondEncoder, request{
		Version: protocolVersion,
		Kind:    requestPaste,
		Paste:   "Write-Output ('" + restoredMarker + "' + (Get-Location).Path)",
	})
	sendTestRequest(t, secondEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	readTestFrame(t, second, secondDecoder, func(value frame) bool {
		plain := ansi.Strip(value.Content)
		return strings.Count(plain, restoredMarker) >= 2 && strings.Contains(plain, "nested workspace")
	})
	_ = second.Close()
	stopRuntime(secondResult)
}

func TestRuntimePreservesExactAgentBindingAcrossStopAndRestartsInAgentDirectory(t *testing.T) {
	fakeBin := t.TempDir()
	const marker = "__SPLIT_RUNTIME_EXACT_RESUME__"
	fakeCodex := filepath.Join(fakeBin, "codex.cmd")
	if err := os.WriteFile(
		fakeCodex,
		[]byte("@echo off\r\necho "+marker+" %CD% %*\r\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	launchRoot := filepath.Join(t.TempDir(), "split checkout")
	agentRoot := filepath.Join(t.TempDir(), "PrismLab")
	for _, directory := range []string{launchRoot, agentRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	statePath := filepath.Join(t.TempDir(), "state.db")
	endpoint, err := Endpoint(statePath)
	if err != nil {
		t.Fatal(err)
	}

	startRuntime := func() chan error {
		result := make(chan error, 1)
		go func() { result <- Run(launchRoot, statePath) }()
		return result
	}
	stopRuntime := func(result chan error) {
		t.Helper()
		if err := Stop(statePath); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runtime did not stop")
		}
	}
	loadPane := func() state.Pane {
		t.Helper()
		store, err := state.Open(statePath)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		snapshot, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Projects) != 1 || len(snapshot.Projects[0].Panes) != 1 {
			t.Fatalf("unexpected exact-resume snapshot: %#v", snapshot)
		}
		return snapshot.Projects[0].Panes[0]
	}
	t.Cleanup(func() { _ = Stop(statePath) })

	firstResult := startRuntime()
	first := dialTestRuntime(t, endpoint)
	firstEncoder := json.NewEncoder(first)
	firstDecoder := json.NewDecoder(first)
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestAttach})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Version == protocolVersion })

	paneID := loadPane().ID
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPaneAgentSession(
		paneID,
		"codex",
		"019f-prismlab-exact-session",
		agentRoot,
	); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Match the user's sequence: detach the UI while the runtime remains live,
	// then explicitly stop the server.
	prefix := tea.Key{Code: 'z', Mod: tea.ModCtrl}
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &prefix})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Content != "" })
	quit := tea.Key{Code: 'q', Text: "q"}
	sendTestRequest(t, firstEncoder, request{Version: protocolVersion, Kind: requestKey, Key: &quit})
	readTestFrame(t, first, firstDecoder, func(value frame) bool { return value.Detach })
	_ = first.Close()
	stopRuntime(firstResult)

	stoppedPane := loadPane()
	if stoppedPane.AgentProvider != "codex" ||
		stoppedPane.AgentSessionID != "019f-prismlab-exact-session" ||
		filepath.Clean(stoppedPane.AgentDirectory) != filepath.Clean(agentRoot) {
		t.Fatalf("server stop lost exact cross-directory binding: %#v", stoppedPane)
	}

	secondResult := startRuntime()
	second := dialTestRuntime(t, endpoint)
	secondEncoder := json.NewEncoder(second)
	secondDecoder := json.NewDecoder(second)
	sendTestRequest(t, secondEncoder, request{Version: protocolVersion, Kind: requestAttach})
	readTestFrame(t, second, secondDecoder, func(value frame) bool {
		plain := ansi.Strip(value.Content)
		return strings.Contains(plain, marker) &&
			strings.Contains(plain, filepath.Base(agentRoot)) &&
			strings.Contains(plain, "resume 019f-prismlab-exact-session")
	})
	logContent, err := os.ReadFile(filepath.Join(filepath.Dir(statePath), "runtime.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, eventName := range []string{
		`"event":"stop_persisted"`,
		`"event":"pane_restore_loaded"`,
		`"event":"agent_resume_scheduled"`,
		`"resume_session_id":"019f-prismlab-exact-session"`,
	} {
		if !strings.Contains(string(logContent), eventName) {
			t.Fatalf("runtime diagnostic log is missing %q: %s", eventName, logContent)
		}
	}
	_ = second.Close()
	stopRuntime(secondResult)
}
func TestRuntimeDiscoversCodexAndTracksWorkingToDone(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	endpoint, err := Endpoint(statePath)
	if err != nil {
		t.Fatal(err)
	}

	helperPath := filepath.Join(t.TempDir(), "codex.exe")
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helperBytes, err := os.ReadFile(testExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helperPath, helperBytes, 0o700); err != nil {
		t.Fatal(err)
	}

	serverResult := make(chan error, 1)
	go func() { serverResult <- Run(root, statePath) }()
	t.Cleanup(func() { _ = Stop(statePath) })

	connection := dialTestRuntime(t, endpoint)
	defer connection.Close()
	encoder := json.NewEncoder(connection)
	decoder := json.NewDecoder(connection)
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestAttach})
	readTestFrame(t, connection, decoder, func(value frame) bool { return value.Version == protocolVersion })

	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestResize, Width: 100, Height: 30})
	readTestFrame(t, connection, decoder, func(value frame) bool { return value.Content != "" })
	enter := tea.Key{Code: tea.KeyEnter}
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	readTestFrame(t, connection, decoder, func(value frame) bool { return value.Content != "" })

	command := fmt.Sprintf("$env:SPLIT_AGENT_TEST_HELPER='1'; & '%s' '-test.run=TestAgentProcessHelper'", helperPath)
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestPaste, Paste: command})
	readTestFrame(t, connection, decoder, func(value frame) bool { return value.Content != "" })
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	readTestFrame(t, connection, decoder, func(value frame) bool {
		return strings.Contains(ansi.Strip(value.Content), "Codex · working")
	})
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load()
	if closeErr := store.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Projects) != 1 || len(snapshot.Projects[0].Panes) != 1 {
		t.Fatalf("unexpected detected-agent snapshot: %#v", snapshot)
	}
	binding := snapshot.Projects[0].Panes[0]
	if binding.AgentProvider != "" || binding.AgentSessionID != "" ||
		binding.AgentDirectory != "" {
		t.Fatalf("process detection invented an ambiguous Codex binding: %#v", binding)
	}
	escape := tea.Key{Code: tea.KeyEscape}
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestKey, Key: &escape})
	readTestFrame(t, connection, decoder, func(value frame) bool {
		return strings.Contains(ansi.Strip(value.Content), "\u00d7 Codex \u00b7 interrup")
	})
	readTestFrame(t, connection, decoder, func(value frame) bool {
		return strings.Contains(ansi.Strip(value.Content), "✓ Codex · done")
	})

	if err := Stop(statePath); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not stop after agent detection test")
	}

	store, err = state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.Load()
	if closeErr := store.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	binding = snapshot.Projects[0].Panes[0]
	if binding.AgentProvider != "" || binding.AgentSessionID != "" ||
		binding.AgentDirectory != "" {
		t.Fatalf("server stop invented an ambiguous Codex binding: %#v", binding)
	}
}

func TestRuntimeDeliversMouseSelectionClipboard(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	endpoint, err := Endpoint(statePath)
	if err != nil {
		t.Fatal(err)
	}

	serverResult := make(chan error, 1)
	go func() { serverResult <- Run(root, statePath) }()
	t.Cleanup(func() { _ = Stop(statePath) })

	connection := dialTestRuntime(t, endpoint)
	defer connection.Close()
	encoder := json.NewEncoder(connection)
	decoder := json.NewDecoder(connection)
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestAttach})
	readTestFrame(t, connection, decoder, func(value frame) bool {
		return value.Version == protocolVersion
	})

	sendTestRequest(t, encoder, request{
		Version: protocolVersion,
		Kind:    requestResize,
		Width:   100,
		Height:  30,
	})
	readTestFrame(t, connection, decoder, func(value frame) bool { return value.Content != "" })
	enter := tea.Key{Code: tea.KeyEnter}
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	readTestFrame(t, connection, decoder, func(value frame) bool { return value.Content != "" })

	const marker = "__SPLIT_RUNTIME_CLIPBOARD_OK__"
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestPaste, Paste: "Write-Output " + marker})
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestKey, Key: &enter})
	output := readTestFrame(t, connection, decoder, func(value frame) bool {
		return strings.Count(ansi.Strip(value.Content), marker) >= 2
	})

	row, column := -1, -1
	for lineIndex, line := range strings.Split(ansi.Strip(output.Content), "\n") {
		if markerIndex := strings.LastIndex(line, marker); markerIndex >= 0 {
			row = lineIndex
			column = ansi.StringWidth(line[:markerIndex])
		}
	}
	if row < 0 || column < 0 {
		t.Fatalf("could not locate marker in runtime frame: %q", ansi.Strip(output.Content))
	}
	start := tea.Mouse{X: column, Y: row, Button: tea.MouseLeft}
	end := tea.Mouse{X: column + len(marker) - 1, Y: row, Button: tea.MouseLeft}
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestClick, Mouse: &start})
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestMotion, Mouse: &end})
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestRelease, Mouse: &end})
	copyKey := tea.Key{Code: 'c', Mod: tea.ModCtrl}
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestKey, Key: &copyKey})
	copied := readTestFrame(t, connection, decoder, func(value frame) bool {
		return value.Clipboard != nil
	})
	if copied.Clipboard == nil || *copied.Clipboard != marker {
		t.Fatalf("runtime clipboard = %#v, want %q", copied.Clipboard, marker)
	}

	if err := Stop(statePath); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not stop after clipboard test")
	}
}
func TestRuntimeCoalescesRapidWheelFrames(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	endpoint, err := Endpoint(statePath)
	if err != nil {
		t.Fatal(err)
	}

	serverResult := make(chan error, 1)
	go func() { serverResult <- Run(root, statePath) }()
	t.Cleanup(func() { _ = Stop(statePath) })

	connection := dialTestRuntime(t, endpoint)
	defer connection.Close()
	encoder := json.NewEncoder(connection)
	decoder := json.NewDecoder(connection)
	sendTestRequest(t, encoder, request{Version: protocolVersion, Kind: requestAttach})
	readTestFrame(t, connection, decoder, func(value frame) bool {
		return value.Version == protocolVersion
	})

	sendTestRequest(t, encoder, request{
		Version: protocolVersion,
		Kind:    requestResize,
		Width:   120,
		Height:  36,
	})
	readTestFrame(t, connection, decoder, func(value frame) bool {
		return value.Content != ""
	})

	observed := make(chan frame, 1024)
	decodeErrors := make(chan error, 1)
	go func() {
		for {
			var value frame
			if err := decoder.Decode(&value); err != nil {
				decodeErrors <- err
				return
			}
			observed <- value
		}
	}()

	_ = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for index := 0; index < 400; index++ {
		button := tea.MouseWheelUp
		if index >= 200 {
			button = tea.MouseWheelDown
		}
		mouse := tea.Mouse{X: 40, Y: 10, Button: button}
		sendTestRequest(t, encoder, request{
			Version: protocolVersion,
			Kind:    requestWheel,
			Mouse:   &mouse,
		})
	}
	_ = connection.SetWriteDeadline(time.Time{})
	time.Sleep(400 * time.Millisecond)

	frames := 0
	for {
		select {
		case <-observed:
			frames++
		default:
			goto drained
		}
	}

drained:
	select {
	case err := <-decodeErrors:
		t.Fatalf("runtime disconnected during wheel burst: %v", err)
	default:
	}
	if frames >= 80 {
		t.Fatalf("wheel burst produced %d full frames; redraws were not coalesced", frames)
	}

	if err := Stop(statePath); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not stop after wheel burst test")
	}
}
func TestAgentProcessHelper(t *testing.T) {
	if os.Getenv("SPLIT_AGENT_TEST_HELPER") != "1" {
		return
	}
	fmt.Print("\x1b[2J\x1b[H• Working (1s • esc to interrupt)")
	time.Sleep(3 * time.Second)
	fmt.Print("\x1b[2J\x1b[H\x1b]2;Codex\x07› ready")
	time.Sleep(8 * time.Second)
}
func dialTestRuntime(t *testing.T, endpoint string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		connection, err := dialEndpoint(ctx, endpoint)
		cancel()
		if err == nil {
			return connection
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("could not connect to test runtime: %v", lastErr)
	return nil
}

func sendTestRequest(t *testing.T, encoder *json.Encoder, value request) {
	t.Helper()
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
}

func readTestFrame(t *testing.T, connection net.Conn, decoder *json.Decoder, accept func(frame) bool) frame {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	lastContent := ""
	_ = connection.SetReadDeadline(deadline)
	for time.Now().Before(deadline) {
		var value frame
		if err := decoder.Decode(&value); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			t.Fatal(err)
		}
		lastContent = ansi.Strip(value.Content)
		if value.Error != "" {
			t.Fatalf("runtime returned an error: %s", value.Error)
		}
		if accept(value) {
			_ = connection.SetReadDeadline(time.Time{})
			return value
		}
	}
	t.Fatalf("timed out waiting for matching runtime frame; last content: %q", lastContent)
	return frame{}
}
