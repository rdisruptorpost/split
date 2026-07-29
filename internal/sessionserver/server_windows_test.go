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

	prefix := tea.Key{Code: 'b', Mod: tea.ModCtrl}
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
