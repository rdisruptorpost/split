package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"split/internal/diagnostics"
)

const schemaVersion = 6

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
	AgentProvider    string
	AgentSessionID   string
	AgentDirectory   string
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
	return filepath.Join(base, "split", "state.db"), nil
}

// MigrateLegacyDefaultDirectory changes the case-preserved Windows app-data
// legacy directory spelling to lowercase "split" without losing the database, WAL, log, or
// managed hook script. Call it only after confirming no older runtime is live.
func MigrateLegacyDefaultDirectory(statePath string) error {
	statePath = filepath.Clean(statePath)
	stateDirectory := filepath.Dir(statePath)
	if !strings.EqualFold(filepath.Base(statePath), "state.db") ||
		!strings.EqualFold(filepath.Base(stateDirectory), "split") {
		return nil
	}
	parent := filepath.Dir(stateDirectory)
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect local app-data directory: %w", err)
	}

	legacyName := ""
	for _, entry := range entries {
		if !entry.IsDir() || !strings.EqualFold(entry.Name(), "split") {
			continue
		}
		if entry.Name() == "split" {
			return nil
		}
		legacyName = entry.Name()
	}
	if legacyName == "" {
		return nil
	}

	legacyPath := filepath.Join(parent, legacyName)
	temporaryPath := filepath.Join(parent, fmt.Sprintf(
		".split-case-migration-%d-%d",
		os.Getpid(),
		time.Now().UnixNano(),
	))
	if err := os.Rename(legacyPath, temporaryPath); err != nil {
		return fmt.Errorf("prepare lowercase app-data migration: %w", err)
	}
	if err := os.Rename(temporaryPath, stateDirectory); err != nil {
		_ = os.Rename(temporaryPath, legacyPath)
		return fmt.Errorf("finish lowercase app-data migration: %w", err)
	}
	return nil
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
	if err := store.migrateLegacySessionFiles(); err != nil {
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
		if _, err := tx.Exec(`ALTER TABLE panes DROP COLUMN profile`); err != nil {
			return fmt.Errorf("remove legacy pane profiles: %w", err)
		}
		version = 3
	}
	if version < 4 {
		// Some pre-1.0 databases had this table while others did not. Creating
		// an empty compatibility table lets one migration preserve useful
		// bindings without branching on sqlite_master.
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_bindings (
			pane_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			session_id TEXT NOT NULL,
			working_directory TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`); err != nil {
			return fmt.Errorf("prepare legacy agent bindings: %w", err)
		}
		if _, err := tx.Exec(`CREATE TABLE pane_agent_sessions (
			pane_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL CHECK (provider IN ('codex', 'claude')),
			session_id TEXT NOT NULL,
			working_directory TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`); err != nil {
			return fmt.Errorf("create pane agent sessions: %w", err)
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO pane_agent_sessions
			(pane_id, provider, session_id, working_directory, updated_at)
			SELECT pane_id, lower(provider), session_id, working_directory, updated_at
			FROM session_bindings
			WHERE lower(provider) IN ('codex', 'claude')
				AND pane_id IN (SELECT id FROM panes)`); err != nil {
			return fmt.Errorf("preserve legacy agent bindings: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE session_bindings`); err != nil {
			return fmt.Errorf("remove legacy agent bindings: %w", err)
		}
		version = 4
	}
	if version < 5 {
		if _, err := tx.Exec(`ALTER TABLE pane_agent_sessions
			ADD COLUMN resume_strategy TEXT NOT NULL DEFAULT 'id'
			CHECK (resume_strategy IN ('id', 'last'))`); err != nil {
			return fmt.Errorf("add agent resume strategy: %w", err)
		}
		version = 5
	}
	if version < 6 {
		if _, err := tx.Exec(`CREATE TABLE pane_agent_sessions_exact (
			pane_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL CHECK (provider IN ('codex', 'claude')),
			session_id TEXT NOT NULL CHECK (length(trim(session_id)) > 0),
			working_directory TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`); err != nil {
			return fmt.Errorf("create exact agent session schema: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO pane_agent_sessions_exact
			(pane_id, provider, session_id, working_directory, updated_at)
			SELECT pane_id, provider, session_id, working_directory, updated_at
			FROM pane_agent_sessions
			WHERE resume_strategy = 'id'
				AND length(trim(session_id)) > 0
				AND pane_id IN (SELECT id FROM panes)`); err != nil {
			return fmt.Errorf("preserve exact agent sessions: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE pane_agent_sessions`); err != nil {
			return fmt.Errorf("remove ambiguous agent session schema: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE pane_agent_sessions_exact
			RENAME TO pane_agent_sessions`); err != nil {
			return fmt.Errorf("activate exact agent session schema: %w", err)
		}
		version = 6
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
		paneRows, err := s.db.Query(`SELECT
				panes.id,
				panes.title,
				panes.working_directory,
				coalesce(pane_agent_sessions.provider, ''),
				coalesce(pane_agent_sessions.session_id, ''),
				coalesce(pane_agent_sessions.working_directory, '')
			FROM panes
			LEFT JOIN pane_agent_sessions ON pane_agent_sessions.pane_id = panes.id
			WHERE panes.project_id = ?
			ORDER BY panes.rowid`, project.ID)
		if err != nil {
			return Snapshot{}, fmt.Errorf("load panes for project %s: %w", project.ID, err)
		}
		for paneRows.Next() {
			var pane Pane
			if err := paneRows.Scan(
				&pane.ID,
				&pane.Title,
				&pane.WorkingDirectory,
				&pane.AgentProvider,
				&pane.AgentSessionID,
				&pane.AgentDirectory,
			); err != nil {
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
	paneCount := 0
	bindingCount := 0
	for _, project := range snapshot.Projects {
		paneCount += len(project.Panes)
		for _, pane := range project.Panes {
			if pane.AgentProvider != "" && pane.AgentSessionID != "" {
				bindingCount++
			}
		}
	}
	_ = diagnostics.Append(
		s.path,
		"state",
		"snapshot_loaded",
		diagnostics.Fields{
			"active_project_id": snapshot.ActiveProjectID,
			"projects":          strconv.Itoa(len(snapshot.Projects)),
			"panes":             strconv.Itoa(paneCount),
			"agent_bindings":    strconv.Itoa(bindingCount),
		},
		nil,
	)
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

	removedResult, err := tx.Exec(`DELETE FROM pane_agent_sessions
		WHERE pane_id NOT IN (SELECT id FROM panes)`)
	if err != nil {
		return fmt.Errorf("remove sessions for closed panes: %w", err)
	}
	removedBindings, _ := removedResult.RowsAffected()

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state save: %w", err)
	}
	paneCount := 0
	for _, project := range snapshot.Projects {
		paneCount += len(project.Panes)
	}
	fields := diagnostics.Fields{
		"active_project_id":      snapshot.ActiveProjectID,
		"projects":               strconv.Itoa(len(snapshot.Projects)),
		"panes":                  strconv.Itoa(paneCount),
		"removed_agent_bindings": strconv.FormatInt(removedBindings, 10),
	}
	var remainingBindings int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pane_agent_sessions`).Scan(&remainingBindings); err == nil {
		fields["agent_bindings"] = strconv.Itoa(remainingBindings)
	}
	_ = diagnostics.Append(s.path, "state", "snapshot_saved", fields, nil)
	return nil
}

// UpdatePaneWorkingDirectory durably records the latest prompt directory
// without rewriting the complete project/layout snapshot.
func (s *Store) UpdatePaneWorkingDirectory(paneID, workingDirectory string) error {
	if strings.TrimSpace(paneID) == "" {
		return errors.New("pane id is empty")
	}
	if !filepath.IsAbs(workingDirectory) {
		return fmt.Errorf("pane working directory is not absolute: %q", workingDirectory)
	}
	result, err := s.db.Exec(`UPDATE panes SET working_directory = ? WHERE id = ?`,
		filepath.Clean(workingDirectory), paneID)
	if err != nil {
		return fmt.Errorf("update pane working directory: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check pane working directory update: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("pane %q is not persisted", paneID)
	}
	return nil
}

// UpsertPaneAgentSession stores the provider-owned session identifier captured
// by a SessionStart hook. The pane must already belong to this database.
func (s *Store) UpsertPaneAgentSession(
	paneID, provider, sessionID, workingDirectory string,
) error {
	return s.upsertPaneAgentSession(
		paneID,
		provider,
		sessionID,
		workingDirectory,
		time.Now().UnixMilli(),
	)
}

func (s *Store) upsertPaneAgentSession(
	paneID, provider, sessionID, workingDirectory string,
	updatedAt int64,
) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	sessionID = strings.TrimSpace(sessionID)
	switch provider {
	case "codex", "claude":
	default:
		return fmt.Errorf("unsupported agent provider %q", provider)
	}
	if strings.TrimSpace(paneID) == "" {
		return errors.New("pane id is empty")
	}
	if sessionID == "" || len(sessionID) > 1024 ||
		strings.ContainsAny(sessionID, "\x00\r\n") {
		return errors.New("agent session id is invalid")
	}
	if !filepath.IsAbs(workingDirectory) {
		return fmt.Errorf("agent working directory is not absolute: %q", workingDirectory)
	}
	if updatedAt <= 0 {
		updatedAt = time.Now().UnixMilli()
	}

	fields := diagnostics.Fields{
		"pane_id":           paneID,
		"provider":          provider,
		"session_id":        sessionID,
		"working_directory": filepath.Clean(workingDirectory),
		"updated_at":        strconv.FormatInt(updatedAt, 10),
	}
	result, err := s.db.Exec(`INSERT INTO pane_agent_sessions
			(pane_id, provider, session_id, working_directory, updated_at)
		SELECT ?, ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM panes WHERE id = ?)
		ON CONFLICT(pane_id) DO UPDATE SET
			provider = excluded.provider,
			session_id = excluded.session_id,
			working_directory = excluded.working_directory,
			updated_at = excluded.updated_at
		WHERE excluded.updated_at >= pane_agent_sessions.updated_at`,
		paneID,
		provider,
		sessionID,
		filepath.Clean(workingDirectory),
		updatedAt,
		paneID,
	)
	if err != nil {
		err = fmt.Errorf("save pane agent session: %w", err)
		_ = diagnostics.Append(s.path, "state", "agent_binding_write_failed", fields, err)
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		err = fmt.Errorf("check pane agent session update: %w", err)
		_ = diagnostics.Append(s.path, "state", "agent_binding_write_failed", fields, err)
		return err
	}
	fields["rows_affected"] = strconv.FormatInt(changed, 10)
	if changed == 0 {
		var exists int
		err := s.db.QueryRow(`SELECT 1 FROM panes WHERE id = ?`, paneID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			err = fmt.Errorf("pane %q is not persisted", paneID)
			_ = diagnostics.Append(s.path, "state", "agent_binding_write_failed", fields, err)
			return err
		}
		if err != nil {
			err = fmt.Errorf("check pane for agent session: %w", err)
			_ = diagnostics.Append(s.path, "state", "agent_binding_write_failed", fields, err)
			return err
		}
	}
	_ = diagnostics.Append(s.path, "state", "agent_binding_written", fields, nil)
	return nil
}

func (s *Store) ClearPaneAgentSession(paneID string) error {
	if strings.TrimSpace(paneID) == "" {
		return errors.New("pane id is empty")
	}
	fields := diagnostics.Fields{"pane_id": paneID}
	result, err := s.db.Exec(`DELETE FROM pane_agent_sessions WHERE pane_id = ?`, paneID)
	if err != nil {
		err = fmt.Errorf("clear pane agent session: %w", err)
		_ = diagnostics.Append(s.path, "state", "agent_binding_clear_failed", fields, err)
		return err
	}
	if changed, err := result.RowsAffected(); err == nil {
		fields["rows_affected"] = strconv.FormatInt(changed, 10)
	}
	_ = diagnostics.Append(s.path, "state", "agent_binding_cleared", fields, nil)
	return nil
}

type legacySessionEvent struct {
	PaneID           string `json:"pane_id"`
	Provider         string `json:"provider"`
	SessionID        string `json:"session_id"`
	WorkingDirectory string `json:"working_directory"`
	CreatedAt        int64  `json:"created_at"`
}

// migrateLegacySessionFiles is the one-time bridge from the short-lived JSON
// hook spool. Once the newest useful event for each existing pane is in
// SQLite, both obsolete split-owned directories are removed.
func (s *Store) migrateLegacySessionFiles() error {
	stateDirectory := filepath.Dir(s.path)
	eventDirectory := filepath.Join(stateDirectory, "session-events")
	entries, err := os.ReadDir(eventDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read legacy session events: %w", err)
	}

	latest := make(map[string]legacySessionEvent)
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(eventDirectory, entry.Name()))
		if err != nil {
			continue
		}
		var event legacySessionEvent
		if json.Unmarshal(content, &event) != nil {
			continue
		}
		event.Provider = strings.ToLower(strings.TrimSpace(event.Provider))
		if event.PaneID == "" || event.SessionID == "" ||
			(event.Provider != "codex" && event.Provider != "claude") ||
			!filepath.IsAbs(event.WorkingDirectory) {
			continue
		}
		previous, exists := latest[event.PaneID]
		if !exists || event.CreatedAt > previous.CreatedAt {
			latest[event.PaneID] = event
		}
	}
	for _, event := range latest {
		// Old files can refer to panes that have since been closed. They are
		// intentionally ignored rather than resurrecting deleted UI state.
		var exists int
		err := s.db.QueryRow(`SELECT 1 FROM panes WHERE id = ?`, event.PaneID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("locate pane for legacy session event: %w", err)
		}
		if err := s.upsertPaneAgentSession(
			event.PaneID,
			event.Provider,
			event.SessionID,
			event.WorkingDirectory,
			event.CreatedAt,
		); err != nil {
			return fmt.Errorf("migrate legacy session event: %w", err)
		}
	}

	for _, name := range []string{"session-events", "session-launches"} {
		_ = os.RemoveAll(filepath.Join(stateDirectory, name))
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
