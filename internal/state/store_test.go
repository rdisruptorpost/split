package state

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStoreRoundTripPreservesWorkspaceMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	snapshot := Snapshot{
		ActiveProjectID: "project-b", SidebarVisible: false, NextProjectNumber: 7,
		Projects: []Project{
			{
				ID: "project-a", Name: "alpha", RootPath: `C:\work\alpha`, ActivePaneID: "pane-a",
				LayoutJSON: []byte(`{"pane_id":"pane-a"}`),
				Panes:      []Pane{{ID: "pane-a", Title: "PowerShell", WorkingDirectory: `C:\work\alpha`}},
			},
			{
				ID: "project-b", Name: "beta", RootPath: `C:\work\beta`, ActivePaneID: "pane-b",
				LayoutJSON: []byte(`{"pane_id":"pane-b"}`),
				Panes:      []Pane{{ID: "pane-b", Title: "PowerShell", WorkingDirectory: `C:\work\beta`}},
			},
		},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, snapshot) {
		t.Fatalf("round trip mismatch\nwant: %#v\n got: %#v", snapshot, loaded)
	}
}

func TestVersionOneAgentStateMigratesToTerminalOnlySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE app_state (singleton INTEGER PRIMARY KEY, active_project_id TEXT NOT NULL, sidebar_visible INTEGER NOT NULL, next_project_number INTEGER NOT NULL)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, root_path TEXT NOT NULL, sort_order INTEGER NOT NULL, active_pane_id TEXT NOT NULL, layout_json TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX projects_sort_order ON projects(sort_order)`,
		`CREATE TABLE panes (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, profile TEXT NOT NULL, title TEXT NOT NULL, working_directory TEXT NOT NULL)`,
		`CREATE INDEX panes_project_id ON panes(project_id)`,
		`CREATE TABLE session_bindings (pane_id TEXT PRIMARY KEY, provider TEXT NOT NULL, session_id TEXT NOT NULL, working_directory TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
		`INSERT INTO app_state VALUES (1, 'project-1', 1, 2)`,
		`INSERT INTO projects VALUES ('project-1', 'legacy', 'C:\work', 0, 'pane-1', '{"pane_id":"pane-1"}')`,
		`INSERT INTO panes VALUES ('pane-1', 'project-1', 'codex', 'Codex', 'C:\work')`,
		`INSERT INTO session_bindings VALUES ('pane-1', 'codex', 'session-1', 'C:\work', 1)`,
		`PRAGMA user_version=1`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			database.Close()
			t.Fatalf("prepare v1 database: %v\nstatement: %s", err, statement)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Projects[0].Panes[0].Title; got != "PowerShell" {
		t.Fatalf("legacy pane title was not normalized: %q", got)
	}
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("unexpected migrated schema version %d: %v", version, err)
	}
	var legacyTableCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_bindings'`).Scan(&legacyTableCount); err != nil {
		t.Fatal(err)
	}
	if legacyTableCount != 0 {
		t.Fatal("legacy session_bindings table should be removed")
	}
}

func TestStoreRejectsDuplicatePaneIDs(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	snapshot := Snapshot{
		ActiveProjectID: "one", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []Project{
			{ID: "one", Name: "one", RootPath: `C:\one`, ActivePaneID: "pane", LayoutJSON: []byte(`{"pane_id":"pane"}`), Panes: []Pane{{ID: "pane", Title: "PowerShell", WorkingDirectory: `C:\one`}}},
			{ID: "two", Name: "two", RootPath: `C:\two`, ActivePaneID: "pane", LayoutJSON: []byte(`{"pane_id":"pane"}`), Panes: []Pane{{ID: "pane", Title: "PowerShell", WorkingDirectory: `C:\two`}}},
		},
	}
	if err := store.Save(snapshot); err == nil {
		t.Fatal("duplicate pane ids should be rejected")
	}
}
