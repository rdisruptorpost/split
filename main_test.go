package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"split/internal/state"
)

func TestSessionHookOutsideSplitIsSilentNoop(t *testing.T) {
	t.Setenv("SPLIT_PANE_ID", "")
	t.Setenv("SPLIT_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	if err := runHook([]string{"session-start", "codex"}); err != nil {
		t.Fatalf("global hook should ignore Codex sessions launched outside Split: %v", err)
	}
}

func TestDecodeSessionStartPayloadAcceptsUTF8BOM(t *testing.T) {
	input := append(
		[]byte{0xef, 0xbb, 0xbf},
		[]byte(`{"session_id":"thr_windows","cwd":"C:\\work"}`)...,
	)
	payload, err := decodeSessionStartPayload(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "thr_windows" || payload.CWD != `C:\work` {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestDecodeSessionStartPayloadAcceptsUTF16LE(t *testing.T) {
	plain := []byte(`{"session_id":"thread-utf16","cwd":"C:\\work"}`)
	input := []byte{0xff, 0xfe}
	for _, value := range plain {
		input = append(input, value, 0)
	}
	payload, err := decodeSessionStartPayload(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "thread-utf16" || payload.CWD != `C:\work` {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestRunHookCommandFailsOpenAndWritesDiagnostic(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("not json"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
		reader.Close()
	}()
	t.Setenv("SPLIT_PANE_ID", "pane-diagnostic")
	t.Setenv("SPLIT_STATE_DB", statePath)

	if err := runHookCommand([]string{"session-start", "codex"}); err != nil {
		t.Fatalf("valid provider hook should fail open: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(statePath), "hook.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("provider=codex pane=pane-diagnostic")) {
		t.Fatalf("diagnostic is missing hook identity: %s", content)
	}
}

func TestSessionHookBindsProviderSessionToPane(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := state.Snapshot{
		ActiveProjectID: "project-1", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []state.Project{{
			ID: "project-1", Name: "work", RootPath: `C:\work`, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes:      []state.Pane{{ID: "pane-1", Profile: "codex", Title: "Codex", WorkingDirectory: `C:\work`}},
		}},
	}
	if err := store.Save(snapshot); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	input := append(
		[]byte{0xef, 0xbb, 0xbf},
		[]byte(`{"session_id":"thr_hooked","cwd":"C:\\work"}`)...,
	)
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
		reader.Close()
	}()
	t.Setenv("SPLIT_PANE_ID", "pane-1")
	t.Setenv("SPLIT_STATE_DB", statePath)

	if err := runHook([]string{"session-start", "codex"}); err != nil {
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
	if got := loaded.Projects[0].Panes[0].ProviderSessionID; got != "thr_hooked" {
		t.Fatalf("hook session id was not persisted: %q", got)
	}
}

func TestSessionHookClaimsLaunchWithoutInheritedEnvironment(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := state.Snapshot{
		ActiveProjectID: "project-1", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []state.Project{{
			ID: "project-1", Name: "work", RootPath: `C:\work`, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes:      []state.Pane{{ID: "pane-1", Profile: "codex", Title: "Codex", WorkingDirectory: `C:\work`}},
		}},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.RegisterSessionLaunch(statePath, "pane-1", "codex", `C:\work`); err != nil {
		t.Fatal(err)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(`{"session_id":"019fa784-9e07-76e0-b202-851e53e4ae0b","cwd":"C:\\work","source":"startup"}`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
		reader.Close()
	}()
	t.Setenv("SPLIT_PANE_ID", "")
	t.Setenv("SPLIT_STATE_DB", statePath)

	if err := runHook([]string{"session-start", "codex"}); err != nil {
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
	if got := loaded.Projects[0].Panes[0].ProviderSessionID; got != "019fa784-9e07-76e0-b202-851e53e4ae0b" {
		t.Fatalf("sanitized hook session was not persisted: %q", got)
	}
}
