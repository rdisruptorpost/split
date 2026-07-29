package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"split/internal/diagnostics"
	"split/internal/layout"
	"split/internal/state"
)

type persistedLayout struct {
	PaneID string           `json:"pane_id,omitempty"`
	Axis   string           `json:"axis,omitempty"`
	Ratio  float64          `json:"ratio,omitempty"`
	First  *persistedLayout `json:"first,omitempty"`
	Second *persistedLayout `json:"second,omitempty"`
}

func Open(root, statePath string) (*Model, error) {
	_ = diagnostics.Append(
		statePath,
		"model",
		"open_started",
		diagnostics.Fields{"launch_root": root},
		nil,
	)
	store, err := state.Open(statePath)
	if err != nil {
		_ = diagnostics.Append(statePath, "model", "state_open_failed", nil, err)
		return nil, err
	}
	model := newModel(root)
	model.store = store

	snapshot, err := store.Load()
	if err != nil {
		_ = diagnostics.Append(statePath, "model", "snapshot_load_failed", nil, err)
		_ = store.Close()
		return nil, err
	}
	if len(snapshot.Projects) == 0 {
		_ = diagnostics.Append(statePath, "model", "default_project_created", nil, nil)
		model.initializeDefaultProject()
		if err := model.saveState(); err != nil {
			_ = diagnostics.Append(statePath, "model", "default_project_save_failed", nil, err)
			model.Close()
			return nil, err
		}
		return model, nil
	}
	if err := model.restoreSnapshot(snapshot); err != nil {
		_ = diagnostics.Append(statePath, "model", "snapshot_restore_failed", nil, err)
		_ = store.Close()
		return nil, fmt.Errorf("restore split state: %w", err)
	}
	_ = diagnostics.Append(
		statePath,
		"model",
		"open_completed",
		diagnostics.Fields{
			"active_project_id": model.active().id,
			"projects":          strconv.Itoa(len(model.tabs)),
			"panes":             strconv.Itoa(len(model.panes)),
		},
		nil,
	)
	return model, nil
}

func (m *Model) persist() {
	if m.store == nil || len(m.tabs) == 0 {
		return
	}
	if err := m.saveState(); err != nil {
		m.notice = "Could not save workspace: " + err.Error()
		_ = diagnostics.Append(m.store.Path(), "model", "snapshot_save_failed", nil, err)
	}
}

func (m *Model) saveState() error {
	if m.store == nil {
		return nil
	}
	snapshot, err := m.stateSnapshot()
	if err != nil {
		return err
	}
	return m.store.Save(snapshot)
}

func (m *Model) stateSnapshot() (state.Snapshot, error) {
	snapshot := state.Snapshot{
		SidebarVisible:    m.sidebarVisible,
		SidebarWidth:      normalizeSidebarWidth(m.sidebarSize),
		NextProjectNumber: m.nextProjectNumber,
	}
	if active := m.active(); active != nil {
		snapshot.ActiveProjectID = active.id
	}
	for _, project := range m.tabs {
		layoutJSON, err := encodeLayout(project.root)
		if err != nil {
			return state.Snapshot{}, fmt.Errorf("encode project %s layout: %w", project.id, err)
		}
		savedProject := state.Project{
			ID:           project.id,
			Name:         project.title,
			RootPath:     project.rootPath,
			ActivePaneID: project.activePane,
			LayoutJSON:   layoutJSON,
		}
		for _, paneID := range project.root.Leaves() {
			item := m.panes[paneID]
			if item == nil {
				return state.Snapshot{}, fmt.Errorf("project %s references missing pane %s", project.id, paneID)
			}
			if item.session != nil {
				observedDirectory := item.session.WorkingDirectory()
				if filepath.IsAbs(observedDirectory) {
					item.cwd = filepath.Clean(observedDirectory)
				}
			}
			paneTitle := strings.TrimSpace(item.title)
			if paneTitle == "" {
				paneTitle = "PowerShell"
			}
			savedProject.Panes = append(savedProject.Panes, state.Pane{
				ID:               item.id,
				Title:            paneTitle,
				WorkingDirectory: item.cwd,
			})
		}
		snapshot.Projects = append(snapshot.Projects, savedProject)
	}
	return snapshot, nil
}

func (m *Model) restoreSnapshot(snapshot state.Snapshot) error {
	m.sidebarVisible = snapshot.SidebarVisible
	m.sidebarSize = normalizeSidebarWidth(snapshot.SidebarWidth)
	m.nextProjectNumber = max(2, snapshot.NextProjectNumber)
	m.tabs = nil
	m.panes = make(map[string]*pane)

	for _, savedProject := range snapshot.Projects {
		root, err := decodeLayout(savedProject.LayoutJSON)
		if err != nil {
			return fmt.Errorf("project %s has invalid layout: %w", savedProject.Name, err)
		}
		projectPaneIDs := make(map[string]struct{}, len(savedProject.Panes))
		for _, savedPane := range savedProject.Panes {
			cwd := savedPane.WorkingDirectory
			if savedPane.AgentProvider != "" && savedPane.AgentSessionID != "" &&
				filepath.IsAbs(savedPane.AgentDirectory) {
				cwd = savedPane.AgentDirectory
			}
			if cwd == "" {
				cwd = savedProject.RootPath
			}
			paneTitle := strings.TrimSpace(savedPane.Title)
			if paneTitle == "" {
				paneTitle = "PowerShell"
			}
			item := &pane{
				id:              savedPane.ID,
				title:           paneTitle,
				kind:            paneTerminal,
				cwd:             cwd,
				resumeProvider:  savedPane.AgentProvider,
				resumeSessionID: savedPane.AgentSessionID,
			}
			if m.store != nil {
				_ = diagnostics.Append(
					m.store.Path(),
					"model",
					"pane_restore_loaded",
					diagnostics.Fields{
						"project_id":                 savedProject.ID,
						"project_name":               savedProject.Name,
						"pane_id":                    savedPane.ID,
						"stored_working_directory":   savedPane.WorkingDirectory,
						"agent_directory":            savedPane.AgentDirectory,
						"selected_working_directory": cwd,
						"resume_provider":            savedPane.AgentProvider,
						"resume_session_id":          savedPane.AgentSessionID,
						"resume_expected":            strconv.FormatBool(savedPane.AgentProvider != "" && savedPane.AgentSessionID != ""),
					},
					nil,
				)
			}
			m.panes[item.id] = item
			projectPaneIDs[item.id] = struct{}{}
			m.absorbID(item.id)
		}
		leaves := root.Leaves()
		if len(leaves) == 0 || len(leaves) != len(projectPaneIDs) {
			return fmt.Errorf("project %s layout and pane list differ", savedProject.Name)
		}
		for _, paneID := range leaves {
			if _, exists := projectPaneIDs[paneID]; !exists {
				return fmt.Errorf("project %s layout references missing pane %s", savedProject.Name, paneID)
			}
		}
		activePane := savedProject.ActivePaneID
		if _, exists := projectPaneIDs[activePane]; !exists {
			activePane = leaves[0]
		}
		project := &tab{
			id:         savedProject.ID,
			title:      savedProject.Name,
			rootPath:   savedProject.RootPath,
			root:       root,
			activePane: activePane,
		}
		m.tabs = append(m.tabs, project)
		m.absorbID(project.id)
	}
	if len(m.tabs) == 0 {
		return errors.New("no valid projects were saved")
	}

	m.activeTab = 0
	for index, project := range m.tabs {
		if project.id == snapshot.ActiveProjectID {
			m.activeTab = index
			break
		}
	}
	m.sidebarCursor = m.activeTab
	m.ensureProjectStarted(m.active())
	m.resizeActivePanes()
	return nil
}

func (m *Model) ensureProjectStarted(project *tab) {
	if project == nil || project.root == nil {
		return
	}
	for _, paneID := range project.root.Leaves() {
		m.startPane(m.panes[paneID])
	}
}

func (m *Model) absorbID(id string) {
	separator := strings.LastIndexByte(id, '-')
	if separator < 0 || separator == len(id)-1 {
		return
	}
	value, err := strconv.Atoi(id[separator+1:])
	if err == nil && value > m.nextID {
		m.nextID = value
	}
}

func encodeLayout(node *layout.Node) ([]byte, error) {
	if node == nil {
		return nil, errors.New("layout is empty")
	}
	return json.Marshal(layoutToState(node))
}

func layoutToState(node *layout.Node) *persistedLayout {
	if node.IsLeaf() {
		return &persistedLayout{PaneID: node.PaneID}
	}
	axis := "columns"
	if node.Axis == layout.Rows {
		axis = "rows"
	}
	return &persistedLayout{
		Axis:   axis,
		Ratio:  node.Ratio,
		First:  layoutToState(node.First),
		Second: layoutToState(node.Second),
	}
}

func decodeLayout(data []byte) (*layout.Node, error) {
	var saved persistedLayout
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	return layoutFromState(&saved)
}

func layoutFromState(saved *persistedLayout) (*layout.Node, error) {
	if saved == nil {
		return nil, errors.New("layout node is missing")
	}
	if saved.PaneID != "" {
		if saved.First != nil || saved.Second != nil || saved.Axis != "" {
			return nil, errors.New("leaf contains split fields")
		}
		return layout.Leaf(saved.PaneID), nil
	}
	if saved.First == nil || saved.Second == nil {
		return nil, errors.New("split is missing a child")
	}
	axis := layout.Columns
	switch saved.Axis {
	case "columns":
	case "rows":
		axis = layout.Rows
	default:
		return nil, fmt.Errorf("unknown split axis %q", saved.Axis)
	}
	first, err := layoutFromState(saved.First)
	if err != nil {
		return nil, err
	}
	second, err := layoutFromState(saved.Second)
	if err != nil {
		return nil, err
	}
	ratio := saved.Ratio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	return &layout.Node{Axis: axis, Ratio: ratio, First: first, Second: second}, nil
}
