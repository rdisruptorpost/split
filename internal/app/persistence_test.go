package app

import (
	"path/filepath"
	"reflect"
	"testing"

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

func TestLegacyAgentPanesRestoreAsPlainTerminals(t *testing.T) {
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
	if item == nil || item.title != "PowerShell" || item.cwd != root {
		t.Fatalf("legacy agent pane was not normalized: %#v", item)
	}
}
func TestLayoutPersistenceRejectsMalformedTrees(t *testing.T) {
	if _, err := decodeLayout([]byte(`{"axis":"columns","first":{"pane_id":"one"}}`)); err == nil {
		t.Fatal("split with a missing child should be rejected")
	}
}
