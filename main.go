package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf16"

	tea "charm.land/bubbletea/v2"

	"split/internal/app"
	"split/internal/hooks"
	"split/internal/state"
)

type sessionStartPayload struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
	Source         string `json:"source"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "split:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "hook":
			return runHookCommand(os.Args[2:])
		case "hooks":
			return runHooks(os.Args[2:])
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}
	statePath, err := state.DefaultPath()
	if err != nil {
		return err
	}
	model, err := app.Open(cwd, statePath)
	if err != nil {
		return err
	}
	defer model.Close()

	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}

func runHook(arguments []string) error {
	if (len(arguments) != 2 && len(arguments) != 3) || arguments[0] != "session-start" {
		return errors.New("usage: split hook session-start <codex|claude> [state-path]")
	}
	provider := arguments[1]
	if provider != "codex" && provider != "claude" {
		return fmt.Errorf("unsupported session provider %q", provider)
	}
	paneID := os.Getenv("SPLIT_PANE_ID")
	statePath := ""
	if len(arguments) == 3 {
		statePath = arguments[2]
	}
	if statePath == "" {
		statePath = os.Getenv("SPLIT_STATE_DB")
	}
	if statePath == "" {
		var err error
		statePath, err = state.DefaultPath()
		if err != nil {
			return err
		}
	}
	if paneID == "" && !state.HasPendingSessionLaunch(statePath, provider) {
		return nil
	}

	payload, err := decodeSessionStartPayload(os.Stdin)
	if err != nil {
		return fmt.Errorf("decode SessionStart hook input: %w", err)
	}
	if payload.SessionID == "" {
		return errors.New("SessionStart hook did not include session_id")
	}
	if paneID == "" {
		_, err := state.ClaimSessionEvent(
			statePath, provider, payload.SessionID, payload.CWD,
			payload.TranscriptPath, payload.Source,
		)
		return err
	}
	return state.QueueSessionEvent(statePath, paneID, provider, payload.SessionID, payload.CWD)
}

func runHookCommand(arguments []string) error {
	err := runHook(arguments)
	if err == nil {
		return nil
	}
	if (len(arguments) != 2 && len(arguments) != 3) || arguments[0] != "session-start" ||
		(arguments[1] != "codex" && arguments[1] != "claude") {
		return err
	}
	statePath := ""
	if len(arguments) == 3 {
		statePath = arguments[2]
	}
	if statePath == "" {
		statePath = os.Getenv("SPLIT_STATE_DB")
	}
	if statePath == "" {
		statePath, _ = state.DefaultPath()
	}
	if statePath != "" {
		_ = state.AppendHookDiagnostic(statePath, arguments[1], os.Getenv("SPLIT_PANE_ID"), err)
	}
	return nil
}

func decodeSessionStartPayload(input io.Reader) (sessionStartPayload, error) {
	const maximumPayload = 1 << 20
	encoded, err := io.ReadAll(io.LimitReader(input, maximumPayload+1))
	if err != nil {
		return sessionStartPayload{}, err
	}
	if len(encoded) > maximumPayload {
		return sessionStartPayload{}, errors.New("SessionStart hook input exceeds 1 MiB")
	}
	encoded, err = normalizeHookEncoding(encoded)
	if err != nil {
		return sessionStartPayload{}, err
	}

	var payload sessionStartPayload
	err = json.NewDecoder(bytes.NewReader(encoded)).Decode(&payload)
	return payload, err
}

func normalizeHookEncoding(encoded []byte) ([]byte, error) {
	if bytes.HasPrefix(encoded, []byte{0xef, 0xbb, 0xbf}) {
		return encoded[3:], nil
	}
	var order binary.ByteOrder
	switch {
	case bytes.HasPrefix(encoded, []byte{0xff, 0xfe}):
		order = binary.LittleEndian
	case bytes.HasPrefix(encoded, []byte{0xfe, 0xff}):
		order = binary.BigEndian
	default:
		return encoded, nil
	}
	encoded = encoded[2:]
	if len(encoded)%2 != 0 {
		return nil, errors.New("UTF-16 SessionStart hook input has an odd byte count")
	}
	units := make([]uint16, len(encoded)/2)
	for index := range units {
		units[index] = order.Uint16(encoded[index*2:])
	}
	return []byte(string(utf16.Decode(units))), nil
}

func runHooks(arguments []string) error {
	if len(arguments) != 1 || arguments[0] != "install" {
		return errors.New("usage: split hooks install")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate Split executable: %w", err)
	}
	paths, err := hooks.DefaultPaths()
	if err != nil {
		return err
	}
	statePath, err := state.DefaultPath()
	if err != nil {
		return err
	}
	results, err := hooks.InstallAll(paths, executable, statePath)
	if err != nil {
		return err
	}
	for _, result := range results {
		status := "already installed"
		if result.Changed {
			status = "installed"
		}
		fmt.Fprintf(os.Stdout, "%s: %s (%s)\n", result.Provider, status, result.Path)
		if result.BackupPath != "" {
			fmt.Fprintf(os.Stdout, "  backup: %s\n", result.BackupPath)
		}
	}
	return nil
}
