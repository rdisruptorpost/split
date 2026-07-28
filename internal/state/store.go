package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schemaVersion = 3

type Snapshot struct {
	ActiveProjectID   string
	SidebarVisible    bool
	NextProjectNumber int
	Projects          []Project
}

type Project struct {
	ID           string
	Name         string
	RootPath     string
	ActivePaneID string
	LayoutJSON   []byte
	Panes        []Pane
}

type Pane struct {
	ID               string
	Title            string
	WorkingDirectory string
}

type Store struct {
	db   *sql.DB
	path string
}

func DefaultPath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		base, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate local application data: %w", err)
		}
	}
	return filepath.Join(base, "Split", "state.db"), nil
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("state database path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db, path: path}
	if err := store.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) configure() error {
	var journalMode string
	if err := s.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
		return fmt.Errorf("enable WAL journal: %w", err)
	}
	for _, statement := range []string{
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("configure state database: %w", err)
		}
	}
	return nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read state schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("state database schema %d is newer than supported schema %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin state migration: %w", err)
	}
	defer tx.Rollback()

	if version == 0 {
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
				sort_order INTEGER NOT NULL,
				active_pane_id TEXT NOT NULL,
				layout_json TEXT NOT NULL
			)`,
			`CREATE UNIQUE INDEX projects_sort_order ON projects(sort_order)`,
			`CREATE TABLE panes (
				id TEXT PRIMARY KEY,
				project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
				profile TEXT NOT NULL,
				title TEXT NOT NULL,
				working_directory TEXT NOT NULL
			)`,
			`CREATE INDEX panes_project_id ON panes(project_id)`,
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("create state schema: %w", err)
			}
		}
		version = 1
	}

	if version < 2 {
		if _, err := tx.Exec(`UPDATE panes SET profile = 'powershell', title = 'PowerShell'`); err != nil {
			return fmt.Errorf("migrate panes to terminal-only profiles: %w", err)
		}
	}
	if version < 3 {
		if _, err := tx.Exec(`DROP TABLE IF EXISTS session_bindings`); err != nil {
			return fmt.Errorf("remove legacy agent bindings: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE panes DROP COLUMN profile`); err != nil {
			return fmt.Errorf("remove legacy pane profiles: %w", err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return fmt.Errorf("record state schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state migration: %w", err)
	}
	return nil
}

func (s *Store) Load() (Snapshot, error) {
	snapshot := Snapshot{SidebarVisible: true, NextProjectNumber: 2}
	row := s.db.QueryRow(`SELECT active_project_id, sidebar_visible, next_project_number
		FROM app_state WHERE singleton = 1`)
	var sidebarVisible int
	if err := row.Scan(&snapshot.ActiveProjectID, &sidebarVisible, &snapshot.NextProjectNumber); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, fmt.Errorf("load app state: %w", err)
		}
	} else {
		snapshot.SidebarVisible = sidebarVisible != 0
	}

	rows, err := s.db.Query(`SELECT id, name, root_path, active_pane_id, layout_json
		FROM projects ORDER BY sort_order`)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load projects: %w", err)
	}
	for rows.Next() {
		var project Project
		var layoutJSON string
		if err := rows.Scan(&project.ID, &project.Name, &project.RootPath, &project.ActivePaneID, &layoutJSON); err != nil {
			rows.Close()
			return Snapshot{}, fmt.Errorf("scan project: %w", err)
		}
		project.LayoutJSON = []byte(layoutJSON)
		snapshot.Projects = append(snapshot.Projects, project)
	}
	if err := rows.Close(); err != nil {
		return Snapshot{}, fmt.Errorf("close project rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("iterate projects: %w", err)
	}

	for projectIndex := range snapshot.Projects {
		project := &snapshot.Projects[projectIndex]
		paneRows, err := s.db.Query(`SELECT id, title, working_directory
			FROM panes WHERE project_id = ? ORDER BY rowid`, project.ID)
		if err != nil {
			return Snapshot{}, fmt.Errorf("load panes for project %s: %w", project.ID, err)
		}
		for paneRows.Next() {
			var pane Pane
			if err := paneRows.Scan(&pane.ID, &pane.Title, &pane.WorkingDirectory); err != nil {
				paneRows.Close()
				return Snapshot{}, fmt.Errorf("scan pane: %w", err)
			}
			project.Panes = append(project.Panes, pane)
		}
		if err := paneRows.Close(); err != nil {
			return Snapshot{}, fmt.Errorf("close pane rows: %w", err)
		}
		if err := paneRows.Err(); err != nil {
			return Snapshot{}, fmt.Errorf("iterate panes: %w", err)
		}
	}
	return snapshot, nil
}

func (s *Store) Save(snapshot Snapshot) error {
	if len(snapshot.Projects) == 0 {
		return errors.New("refusing to persist an empty project list")
	}
	if snapshot.NextProjectNumber < 2 {
		snapshot.NextProjectNumber = 2
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin state save: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM panes"); err != nil {
		return fmt.Errorf("clear saved panes: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM projects"); err != nil {
		return fmt.Errorf("clear saved projects: %w", err)
	}

	projectIDs := make(map[string]struct{}, len(snapshot.Projects))
	paneIDs := make(map[string]struct{})
	for projectIndex, project := range snapshot.Projects {
		if project.ID == "" || project.Name == "" || project.RootPath == "" || project.ActivePaneID == "" || len(project.LayoutJSON) == 0 {
			return fmt.Errorf("project at position %d is incomplete", projectIndex)
		}
		if _, exists := projectIDs[project.ID]; exists {
			return fmt.Errorf("duplicate project id %q", project.ID)
		}
		projectIDs[project.ID] = struct{}{}
		if _, err := tx.Exec(`INSERT INTO projects
			(id, name, root_path, sort_order, active_pane_id, layout_json)
			VALUES (?, ?, ?, ?, ?, ?)`,
			project.ID, project.Name, project.RootPath, projectIndex, project.ActivePaneID, string(project.LayoutJSON)); err != nil {
			return fmt.Errorf("save project %s: %w", project.ID, err)
		}

		for _, pane := range project.Panes {
			if pane.ID == "" || pane.Title == "" || pane.WorkingDirectory == "" {
				return fmt.Errorf("project %s contains an incomplete pane", project.ID)
			}
			if _, exists := paneIDs[pane.ID]; exists {
				return fmt.Errorf("duplicate pane id %q", pane.ID)
			}
			paneIDs[pane.ID] = struct{}{}
			if _, err := tx.Exec(`INSERT INTO panes
				(id, project_id, title, working_directory)
				VALUES (?, ?, ?, ?)`,
				pane.ID, project.ID, pane.Title, pane.WorkingDirectory); err != nil {
				return fmt.Errorf("save pane %s: %w", pane.ID, err)
			}

		}
	}

	if _, err := tx.Exec(`INSERT INTO app_state
		(singleton, active_project_id, sidebar_visible, next_project_number)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET
			active_project_id = excluded.active_project_id,
			sidebar_visible = excluded.sidebar_visible,
			next_project_number = excluded.next_project_number`,
		snapshot.ActiveProjectID, boolInt(snapshot.SidebarVisible), snapshot.NextProjectNumber); err != nil {
		return fmt.Errorf("save app state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state save: %w", err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
