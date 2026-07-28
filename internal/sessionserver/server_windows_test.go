//go:build windows

package sessionserver

import (
	"context"
	"encoding/json"
	"net"
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
