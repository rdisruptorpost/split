package app

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"split/internal/layout"
	"split/internal/state"
)

func TestPersistentModelRestoresProjectOrderNamesAndPaneLayouts(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	model, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = model.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	model.splitActive(layout.Columns)
	model.splitActive(layout.Rows)
	model.newProject()

	model.tabs[0].title = "primary"
	model.tabs[1].title = "secondary"
	model.tabs[0], model.tabs[1] = model.tabs[1], model.tabs[0]
	model.activeTab = 1
	model.sidebarCursor = 1
	model.sidebarVisible = false
	model.tabs[1].activePane = model.tabs[1].root.Leaves()[0]
	model.persist()

	before, err := model.stateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	model.Close()

	restored, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	after, err := restored.stateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("restored model differs from saved state\nwant: %#v\n got: %#v", before, after)
	}
	if restored.active().title != "primary" {
		t.Fatalf("active project was not restored: %q", restored.active().title)
	}
	if restored.sidebarVisible {
		t.Fatal("sidebar visibility was not restored")
	}
	if got := len(restored.tabs[1].root.Leaves()); got != 3 {
		t.Fatalf("split tree was not restored, got %d panes", got)
	}
	if restored.tabs[0].root.Leaves()[0] == restored.tabs[1].root.Leaves()[0] {
		t.Fatal("projects should retain distinct stable pane ids")
	}
}

func TestProjectRenameIsPersisted(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	model, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	model.openProjectRenameDialog(0)
	model.insertProjectRenameText("persistent project")
	model.confirmProjectRename()
	if model.renameDialog.open {
		t.Fatal("confirming a valid name should close the dialog")
	}
	model.Close()

	restored, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got := restored.tabs[0].title; got != "persistent project" {
		t.Fatalf("restored project name = %q", got)
	}
}

func TestTerminalRenameIsPersistedAsAPlainTerminalLabel(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	model, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	paneID := model.active().activePane
	model.openPaneRenameDialog(paneID, modeNavigate)
	model.insertProjectRenameText("review agent")
	model.confirmProjectRename()
	if got := model.panes[paneID].title; got != "review agent" {
		t.Fatalf("renamed terminal title = %q", got)
	}
	model.Close()

	restored, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	item := restored.panes[paneID]
	if item == nil || item.title != "review agent" {
		t.Fatalf("restored terminal title = %#v", item)
	}
	if item.kind != paneTerminal {
		t.Fatalf("a custom label must still restore as a plain terminal, got kind %v", item.kind)
	}
}
func TestClosedProjectIsRemovedFromPersistentState(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	model, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	closedProjectID := model.tabs[0].id
	model.newProject()
	survivingProjectID := model.tabs[1].id
	if !model.closeProject(0) {
		t.Fatal("expected the first of two projects to close")
	}
	model.Close()

	restored, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if len(restored.tabs) != 1 || restored.tabs[0].id != survivingProjectID {
		t.Fatalf("restored projects = %#v, want only %q", restored.tabs, survivingProjectID)
	}
	if restored.tabs[0].id == closedProjectID {
		t.Fatal("closed project returned after restart")
	}
}
func TestRestoredInactiveProjectStartsLazilyWhenSelected(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	model, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	model.newProject()
	model.selectTab(0)
	model.Close()

	restored, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()

	inactive := restored.tabs[1]
	for _, paneID := range inactive.root.Leaves() {
		if restored.panes[paneID].started {
			t.Fatal("inactive project should not spawn processes during startup")
		}
	}
	restored.selectTab(1)
	for _, paneID := range inactive.root.Leaves() {
		if !restored.panes[paneID].started || restored.panes[paneID].session == nil {
			t.Fatal("selecting a restored project should start its saved panes")
		}
	}
}

func TestSavedPaneTitlesRestoreAsPlainTerminalLabels(t *testing.T) {
	root := t.TempDir()
	model := newModel(root)
	snapshot := state.Snapshot{
		ActiveProjectID: "tab-1", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []state.Project{{
			ID: "tab-1", Name: "legacy", RootPath: root, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes: []state.Pane{{
				ID: "pane-1", Title: "Codex", WorkingDirectory: root,
			}},
		}},
	}
	if err := model.restoreSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	defer model.Close()

	item := model.activePane()
	if item == nil || item.title != "Codex" || item.kind != paneTerminal || item.cwd != root {
		t.Fatalf("saved pane title was not restored as a plain terminal label: %#v", item)
	}
}
func TestLayoutPersistenceRejectsMalformedTrees(t *testing.T) {
	if _, err := decodeLayout([]byte(`{"axis":"columns","first":{"pane_id":"one"}}`)); err == nil {
		t.Fatal("split with a missing child should be rejected")
	}
}

func TestPersistentModelRunsNativeResumeForCapturedAgentSession(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell resume integration is Windows-specific")
	}
	fakeBin := t.TempDir()
	const marker = "__SPLIT_FAKE_CODEX_RESUME__"
	fakeCodex := filepath.Join(fakeBin, "codex.cmd")
	if err := os.WriteFile(fakeCodex, []byte("@echo off\r\necho "+marker+" %*\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := state.Snapshot{
		ActiveProjectID: "tab-1", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []state.Project{{
			ID: "tab-1", Name: "resume", RootPath: root, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes: []state.Pane{{
				ID: "pane-1", Title: "PowerShell", WorkingDirectory: root,
			}},
		}},
	}
	if err := store.Save(snapshot); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.UpsertPaneAgentSession("pane-1", "codex", "session-42", root); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	model, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	item := model.panes["pane-1"]
	if item == nil || item.session == nil {
		model.Close()
		t.Fatalf("restored pane did not start: %#v", item)
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for !strings.Contains(item.session.Render(), marker+" resume session-42") {
		select {
		case <-model.TerminalEvents():
		case <-timer.C:
			content := item.session.Render()
			model.Close()
			t.Fatalf("captured Codex session was not resumed: %q", content)
		}
	}
	model.Close()

	store, err = state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Projects[0].Panes[0].AgentSessionID; got != "session-42" {
		t.Fatalf("server-style close lost resumable provider session: %q", got)
	}
}
