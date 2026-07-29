package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"split/internal/state"
)

func TestRecordSessionStartAcceptsPowerShellUTF8BOMAndPersistsBinding(t *testing.T) {
	directory := t.TempDir()
	statePath := filepath.Join(directory, "state.db")
	workingDirectory := filepath.Join(directory, "workspace")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := state.Snapshot{
		ActiveProjectID:   "project-1",
		SidebarVisible:    true,
		NextProjectNumber: 2,
		Projects: []state.Project{{
			ID: "project-1", Name: "workspace", RootPath: workingDirectory,
			ActivePaneID: "pane-1", LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes: []state.Pane{{
				ID: "pane-1", Title: "PowerShell", WorkingDirectory: workingDirectory,
			}},
		}},
	}
	if err := store.Save(snapshot); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	payload := "\ufeff" + `{"session_id":"019fa900-e81f-7061-b8c3-948b6c301456","cwd":` +
		`"` + strings.ReplaceAll(workingDirectory, `\`, `\\`) +
		`","hook_event_name":"SessionStart"}`
	if err := RecordSessionStart(
		"codex", "pane-1", statePath, strings.NewReader(payload),
	); err != nil {
		t.Fatal(err)
	}

	store, err = state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	pane := loaded.Projects[0].Panes[0]
	if pane.AgentProvider != "codex" ||
		pane.AgentSessionID != "019fa900-e81f-7061-b8c3-948b6c301456" ||
		pane.AgentDirectory != workingDirectory {
		t.Fatalf("recorded pane session = %#v", pane)
	}
	logContent, err := os.ReadFile(filepath.Join(directory, "runtime.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		`"event":"payload_received"`,
		`"event":"binding_written"`,
		`"event":"agent_binding_written"`,
		`"session_id":"019fa900-e81f-7061-b8c3-948b6c301456"`,
	} {
		if !strings.Contains(string(logContent), marker) {
			t.Fatalf("session diagnostic log is missing %q: %s", marker, logContent)
		}
	}
}

func TestRecordSessionStartRejectsUnrelatedPane(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	payload := `{"session_id":"session-1","cwd":"C:\\workspace","hook_event_name":"SessionStart"}`
	if err := RecordSessionStart(
		"claude", "not-a-split-pane", statePath, strings.NewReader(payload),
	); err == nil {
		t.Fatal("a session hook must not create bindings for an unknown pane")
	}
}
