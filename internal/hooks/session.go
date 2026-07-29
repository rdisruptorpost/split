package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"split/internal/diagnostics"
	"split/internal/state"
)

const maxSessionHookPayload = 1 << 20

type sessionStartPayload struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
}

// RecordSessionStart persists one provider-owned session directly in SQLite.
// The stable PowerShell hook calls this only when it inherited a split pane's
// environment, so sessions started in unrelated terminals are untouched.
func RecordSessionStart(
	provider, paneID, statePath string,
	input io.Reader,
) error {
	fields := diagnostics.Fields{
		"provider": provider,
		"pane_id":  paneID,
	}
	fail := func(eventName string, err error) error {
		_ = diagnostics.Append(statePath, "session-capture", eventName, fields, err)
		return err
	}

	if strings.TrimSpace(paneID) == "" {
		return fail("validation_failed", errors.New("split pane id is empty"))
	}
	if strings.TrimSpace(statePath) == "" {
		return fail("validation_failed", errors.New("split state path is empty"))
	}
	if input == nil {
		return fail("validation_failed", errors.New("session hook input is empty"))
	}

	content, err := io.ReadAll(io.LimitReader(input, maxSessionHookPayload+1))
	if err != nil {
		return fail("payload_read_failed", fmt.Errorf("read SessionStart payload: %w", err))
	}
	fields["payload_bytes"] = strconv.Itoa(len(content))
	if len(content) > maxSessionHookPayload {
		return fail("payload_rejected", errors.New("SessionStart payload exceeds size limit"))
	}
	// Windows PowerShell 5.1 can prefix text piped to a native executable with
	// a UTF-8 BOM. encoding/json intentionally rejects it, so normalize only
	// that transport marker before decoding Codex's JSON object.
	hadBOM := bytes.HasPrefix(content, []byte{0xef, 0xbb, 0xbf})
	fields["utf8_bom"] = strconv.FormatBool(hadBOM)
	content = bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})
	_ = diagnostics.Append(statePath, "session-capture", "payload_received", fields, nil)

	var payload sessionStartPayload
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&payload); err != nil {
		return fail("payload_decode_failed", fmt.Errorf("decode SessionStart payload: %w", err))
	}
	fields["session_id"] = strings.TrimSpace(payload.SessionID)
	fields["payload_cwd"] = strings.TrimSpace(payload.CWD)
	fields["hook_event"] = payload.HookEventName
	if payload.HookEventName != "" &&
		!strings.EqualFold(payload.HookEventName, "SessionStart") {
		return fail(
			"payload_rejected",
			fmt.Errorf("unexpected hook event %q", payload.HookEventName),
		)
	}
	workingDirectory := strings.TrimSpace(payload.CWD)
	if workingDirectory == "" {
		workingDirectory, err = os.Getwd()
		if err != nil {
			return fail(
				"working_directory_failed",
				fmt.Errorf("determine hook working directory: %w", err),
			)
		}
		fields["working_directory_source"] = "process"
	} else {
		fields["working_directory_source"] = "payload"
	}
	if !filepath.IsAbs(workingDirectory) {
		return fail(
			"working_directory_rejected",
			fmt.Errorf("hook working directory is not absolute: %q", workingDirectory),
		)
	}
	workingDirectory = filepath.Clean(workingDirectory)
	fields["working_directory"] = workingDirectory

	store, err := state.Open(statePath)
	if err != nil {
		return fail("database_open_failed", err)
	}
	defer store.Close()
	_ = diagnostics.Append(statePath, "session-capture", "binding_write_started", fields, nil)
	if err := store.UpsertPaneAgentSession(
		paneID,
		provider,
		payload.SessionID,
		workingDirectory,
	); err != nil {
		return fail("binding_write_failed", err)
	}
	_ = diagnostics.Append(statePath, "session-capture", "binding_written", fields, nil)
	return nil
}
