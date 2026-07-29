package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStoreRoundTripPreservesWorkspaceMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	snapshot := Snapshot{
		ActiveProjectID: "project-b", SidebarVisible: false, SidebarWidth: 37, NextProjectNumber: 7,
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

func TestVersionOneAgentStateMigratesToSQLiteSessionSchema(t *testing.T) {
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
	pane := loaded.Projects[0].Panes[0]
	if pane.Title != "PowerShell" {
		t.Fatalf("legacy pane title was not normalized: %q", pane.Title)
	}
	if pane.AgentProvider != "codex" || pane.AgentSessionID != "session-1" ||
		pane.AgentDirectory != `C:\work` {
		t.Fatalf("legacy agent binding was not preserved: %#v", pane)
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

func TestPaneDirectoryAndAgentSessionUpdatesSurviveSnapshotSaves(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	snapshot := Snapshot{
		ActiveProjectID: "one", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []Project{{
			ID: "one", Name: "one", RootPath: `C:\one`, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes: []Pane{{
				ID: "pane-1", Title: "PowerShell", WorkingDirectory: `C:\one`,
			}},
		}},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePaneWorkingDirectory("pane-1", `C:\two`); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPaneAgentSession(
		"pane-1", "claude", "session-42", `C:\two`,
	); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	pane := loaded.Projects[0].Panes[0]
	if pane.WorkingDirectory != `C:\two` || pane.AgentProvider != "claude" ||
		pane.AgentSessionID != "session-42" || pane.AgentDirectory != `C:\two` {
		t.Fatalf("updated pane metadata = %#v", pane)
	}

	// Saving layout state must preserve a binding that can be written by a
	// separate hook process while the runtime is alive.
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Projects[0].Panes[0].AgentSessionID; got != "session-42" {
		t.Fatalf("agent session was lost during snapshot save: %q", got)
	}
	if err := store.ClearPaneAgentSession("pane-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	pane = loaded.Projects[0].Panes[0]
	if pane.AgentProvider != "" || pane.AgentSessionID != "" ||
		pane.AgentDirectory != "" {
		t.Fatalf("cleared pane session remains: %#v", pane)
	}
}

func TestLegacySessionJSONMigratesToSQLiteAndIsRemoved(t *testing.T) {
	directory := t.TempDir()
	statePath := filepath.Join(directory, "state.db")
	store, err := Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{
		ActiveProjectID: "one", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []Project{{
			ID: "one", Name: "one", RootPath: `C:\one`, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes: []Pane{{
				ID: "pane-1", Title: "PowerShell", WorkingDirectory: `C:\one`,
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

	eventDirectory := filepath.Join(directory, "session-events")
	launchDirectory := filepath.Join(directory, "session-launches")
	if err := os.MkdirAll(eventDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(launchDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index, event := range []legacySessionEvent{
		{PaneID: "pane-1", Provider: "codex", SessionID: "old", WorkingDirectory: `C:\one`, CreatedAt: 10},
		{PaneID: "pane-1", Provider: "codex", SessionID: "new", WorkingDirectory: `C:\two`, CreatedAt: 20},
		{PaneID: "closed-pane", Provider: "claude", SessionID: "stale", WorkingDirectory: `C:\old`, CreatedAt: 30},
	} {
		content, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(eventDirectory, fmt.Sprintf("%d.json", index)), content, 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(launchDirectory, "launch.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err = Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	pane := loaded.Projects[0].Panes[0]
	if pane.AgentProvider != "codex" || pane.AgentSessionID != "new" ||
		pane.AgentDirectory != `C:\two` {
		t.Fatalf("newest legacy event was not migrated: %#v", pane)
	}
	for _, path := range []string{eventDirectory, launchDirectory} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy directory still exists: %s (%v)", path, err)
		}
	}
}

func TestV5MigrationDiscardsAmbiguousFallbackAndPreservesExactSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE app_state (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			active_project_id TEXT NOT NULL,
			sidebar_visible INTEGER NOT NULL,
			next_project_number INTEGER NOT NULL
		)`,
		`CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			root_path TEXT NOT NULL,
			sort_order INTEGER NOT NULL UNIQUE,
			active_pane_id TEXT NOT NULL,
			layout_json BLOB NOT NULL
		)`,
		`CREATE TABLE panes (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			title TEXT NOT NULL,
			working_directory TEXT NOT NULL
		)`,
		`CREATE TABLE pane_agent_sessions (
			pane_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			session_id TEXT NOT NULL,
			working_directory TEXT NOT NULL,
			updated_at INTEGER NOT NULL,
			resume_strategy TEXT NOT NULL
		)`,
		`INSERT INTO app_state VALUES (1, 'one', 1, 2)`,
		`INSERT INTO projects VALUES ('one', 'one', 'C:\one', 0, 'pane-fallback',
			'{"pane_id":"pane-fallback"}')`,
		`INSERT INTO panes VALUES ('pane-fallback', 'one', 'PowerShell', 'C:\one')`,
		`INSERT INTO panes VALUES ('pane-exact', 'one', 'PowerShell', 'C:\one')`,
		`INSERT INTO pane_agent_sessions VALUES
			('pane-fallback', 'codex', '', 'C:\fallback', 1, 'last')`,
		`INSERT INTO pane_agent_sessions VALUES
			('pane-exact', 'codex', 'exact-session-42', 'C:\exact', 2, 'id')`,
		`PRAGMA user_version=5`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			database.Close()
			t.Fatalf("prepare v5 database: %v\nstatement: %s", err, statement)
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
	if len(loaded.Projects) != 1 || len(loaded.Projects[0].Panes) != 2 {
		t.Fatalf("unexpected migrated snapshot: %#v", loaded)
	}
	byID := make(map[string]Pane)
	for _, pane := range loaded.Projects[0].Panes {
		byID[pane.ID] = pane
	}
	fallback := byID["pane-fallback"]
	if fallback.AgentProvider != "" || fallback.AgentSessionID != "" ||
		fallback.AgentDirectory != "" {
		t.Fatalf("ambiguous fallback survived v6 migration: %#v", fallback)
	}
	exact := byID["pane-exact"]
	if exact.AgentProvider != "codex" || exact.AgentSessionID != "exact-session-42" ||
		exact.AgentDirectory != `C:\exact` {
		t.Fatalf("exact session was not preserved: %#v", exact)
	}
	var strategyColumns int
	if err := store.db.QueryRow(`SELECT COUNT(*)
		FROM pragma_table_info('pane_agent_sessions')
		WHERE name = 'resume_strategy'`).Scan(&strategyColumns); err != nil {
		t.Fatal(err)
	}
	if strategyColumns != 0 {
		t.Fatal("v6 schema still contains resume_strategy")
	}
}
func TestMigrateLegacyDefaultDirectoryPreservesFilesAndLowercasesName(t *testing.T) {
	parent := t.TempDir()
	legacyDirectory := filepath.Join(parent, "Split")
	if err := os.MkdirAll(legacyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"state.db":         "database",
		"state.db-wal":     "wal",
		"runtime.log":      "log",
		"session-hook.ps1": "hook",
	} {
		if err := os.WriteFile(filepath.Join(legacyDirectory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	statePath := filepath.Join(parent, "split", "state.db")
	if err := MigrateLegacyDefaultDirectory(statePath); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !reflect.DeepEqual(names, []string{"split"}) {
		t.Fatalf("app-data directory names = %#v, want lowercase split", names)
	}
	for name, want := range map[string]string{
		"state.db":         "database",
		"state.db-wal":     "wal",
		"runtime.log":      "log",
		"session-hook.ps1": "hook",
	} {
		content, err := os.ReadFile(filepath.Join(parent, "split", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != want {
			t.Fatalf("migrated %s = %q, want %q", name, content, want)
		}
	}
}

func TestProviderUsageCacheSurvivesWorkspaceSavesAndIgnoresOlderWrites(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	newer := ProviderUsage{
		Provider:    "codex",
		UsedPercent: 28.4,
		ResetsAt:    time.UnixMilli(1_800_100_000_000),
		UpdatedAt:   time.UnixMilli(1_800_000_002_000),
	}
	if err := store.UpsertProviderUsage(newer); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProviderUsage(ProviderUsage{
		Provider:    "codex",
		UsedPercent: 99,
		UpdatedAt:   time.UnixMilli(1_800_000_001_000),
	}); err != nil {
		t.Fatal(err)
	}

	snapshot := Snapshot{
		ActiveProjectID: "one", SidebarVisible: true, SidebarWidth: 24, NextProjectNumber: 2,
		Projects: []Project{{
			ID: "one", Name: "one", RootPath: `C:\one`, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes: []Pane{{
				ID: "pane-1", Title: "PowerShell", WorkingDirectory: `C:\one`,
			}},
		}},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadProviderUsage()
	if err != nil {
		t.Fatal(err)
	}
	got, exists := loaded["codex"]
	if !exists || got.UsedPercent != newer.UsedPercent ||
		!got.ResetsAt.Equal(newer.ResetsAt) || !got.UpdatedAt.Equal(newer.UpdatedAt) {
		t.Fatalf("cached Codex usage = %#v, want %#v", got, newer)
	}
}
