package app

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"split/internal/layout"
	"split/internal/terminal"
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

func TestAgentCommandsUseStableSessionHandles(t *testing.T) {
	model := newModel(t.TempDir())
	model.launchOptions = []launchOption{
		{profile: profileCodex, title: "Codex", command: terminal.Command{Name: "codex"}, available: true},
		{profile: profileClaude, title: "Claude Code", command: terminal.Command{Name: "claude"}, available: true},
	}

	codex := &pane{id: "pane-codex", profile: profileCodex, cwd: model.root, providerSessionID: "thr_123", resumeSession: true}
	command, err := model.commandForPane(codex)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(command.Args, []string{"resume", "thr_123"}) {
		t.Fatalf("unexpected Codex resume args: %#v", command.Args)
	}
	if command.Env["SPLIT_PANE_ID"] != codex.id || command.Env["SPLIT_PROVIDER"] != "codex" {
		t.Fatalf("Codex hook metadata is incomplete: %#v", command.Env)
	}

	claudeNew := &pane{id: "pane-claude-new", profile: profileClaude, cwd: model.root, providerSessionID: "uuid-new"}
	command, err = model.commandForPane(claudeNew)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(command.Args, []string{"--session-id", "uuid-new"}) {
		t.Fatalf("unexpected new Claude session args: %#v", command.Args)
	}

	claudeRestored := &pane{id: "pane-claude-old", profile: profileClaude, cwd: model.root, providerSessionID: "uuid-old", resumeSession: true}
	command, err = model.commandForPane(claudeRestored)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(command.Args, []string{"--resume", "uuid-old"}) {
		t.Fatalf("unexpected Claude resume args: %#v", command.Args)
	}
}

func TestLayoutPersistenceRejectsMalformedTrees(t *testing.T) {
	if _, err := decodeLayout([]byte(`{"axis":"columns","first":{"pane_id":"one"}}`)); err == nil {
		t.Fatal("split with a missing child should be rejected")
	}
}
