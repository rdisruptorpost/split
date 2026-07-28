package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"split/internal/layout"
	"split/internal/state"
	"split/internal/terminal"
)

const (
	defaultWidth           = 120
	defaultHeight          = 36
	sidebarWidth           = 24
	sidebarProjectStart    = 4
	statusHeight           = 1
	agentMinimumBodyWidth  = 38
	agentMinimumBodyHeight = 8
)

type mode uint8

const (
	modeNavigate mode = iota
	modeTerminal
	modePrefix
)

type focusArea uint8

const (
	focusPanes focusArea = iota
	focusSidebar
)

type paneKind uint8

const (
	paneOverview paneKind = iota
	paneTerminal
)

type paneProfile uint8

const (
	profileShell paneProfile = iota
	profileCodex
	profileClaude
)

type launchOption struct {
	profile     paneProfile
	title       string
	description string
	command     terminal.Command
	available   bool
}

type pane struct {
	id                string
	title             string
	kind              paneKind
	profile           paneProfile
	cwd               string
	providerSessionID string
	resumeSession     bool
	started           bool
	session           *terminal.Session
	err               error
}

type tab struct {
	id         string
	title      string
	rootPath   string
	root       *layout.Node
	activePane string
}

type Model struct {
	width  int
	height int
	root   string

	mode             mode
	modeBeforePrefix mode
	focus            focusArea
	sidebarVisible   bool
	sidebarCursor    int

	tabs              []*tab
	activeTab         int
	panes             map[string]*pane
	nextID            int
	nextProjectNumber int

	events chan terminal.Event
	notice string

	launchOptions    []launchOption
	launcherOpen     bool
	launcherSelected int
	contextMenu      paneContextMenuState

	store *state.Store
}

type terminalBatchMsg struct {
	events []terminal.Event
}

func discoverLaunchOptions(root string) []launchOption {
	codexCommand, codexErr := terminal.ResolveCommand("codex", root)
	claudeCommand, claudeErr := terminal.ResolveCommand("claude", root)
	return []launchOption{
		{
			profile: profileShell, title: "PowerShell",
			description: "A general-purpose shell in this workspace",
			command:     terminal.DefaultShell(root), available: true,
		},
		{
			profile: profileCodex, title: "Codex",
			description: "OpenAI Codex CLI attached to this repository",
			command:     codexCommand, available: codexErr == nil,
		},
		{
			profile: profileClaude, title: "Claude Code",
			description: "Claude Code CLI attached to this repository",
			command:     claudeCommand, available: claudeErr == nil,
		},
	}
}

func New(root string) *Model {
	model := newModel(root)
	model.initializeDefaultProject()
	return model
}

func newModel(root string) *Model {
	model := &Model{
		width:             defaultWidth,
		height:            defaultHeight,
		root:              root,
		mode:              modeNavigate,
		focus:             focusPanes,
		sidebarVisible:    true,
		panes:             make(map[string]*pane),
		events:            make(chan terminal.Event, 128),
		nextProjectNumber: 2,
	}
	model.launchOptions = discoverLaunchOptions(root)
	return model
}

func (m *Model) initializeDefaultProject() {
	shell := m.newTerminalPane("PowerShell")
	m.tabs = []*tab{{
		id:         m.newID("tab"),
		title:      m.projectName(),
		rootPath:   shell.cwd,
		root:       layout.Leaf(shell.id),
		activePane: shell.id,
	}}
	m.activeTab = 0
	m.sidebarCursor = 0
	m.resizeActivePanes()
}

func (m *Model) Init() tea.Cmd {
	return waitForTerminalEvents(m.events)
}

func (m *Model) Close() {
	m.persist()
	for _, item := range m.panes {
		if item.session != nil {
			item.session.Close()
		}
	}
	if m.store != nil {
		_ = m.store.Close()
		m.store = nil
	}
}

func (m *Model) newID(prefix string) string {
	m.nextID++
	return fmt.Sprintf("%s-%d", prefix, m.nextID)
}

func (m *Model) newOverviewPane() *pane {
	item := &pane{
		id:    m.newID("pane"),
		title: "Command center",
		kind:  paneOverview,
	}
	m.panes[item.id] = item
	return item
}

func (m *Model) newTerminalPane(title string) *pane {
	return m.newPane(title, profileShell, "", false)
}

func (m *Model) newProfilePane(profile paneProfile) *pane {
	for _, option := range m.launchOptions {
		if option.profile != profile {
			continue
		}
		if !option.available {
			m.notice = option.title + " was not found on PATH"
			return nil
		}
		sessionID := ""
		if profile == profileClaude {
			var err error
			sessionID, err = newSessionUUID()
			if err != nil {
				m.notice = "Could not allocate a Claude session id: " + err.Error()
				return nil
			}
		}
		return m.newPane(option.title, option.profile, sessionID, false)
	}
	m.notice = "Unknown launch profile"
	return nil
}

func (m *Model) newPane(title string, profile paneProfile, providerSessionID string, resume bool) *pane {
	item := &pane{
		id:                m.newID("pane"),
		title:             title,
		kind:              paneTerminal,
		profile:           profile,
		cwd:               m.activeProjectRoot(),
		providerSessionID: providerSessionID,
		resumeSession:     resume,
	}
	m.panes[item.id] = item
	m.startPane(item)
	return item
}

func (m *Model) startPane(item *pane) {
	if item == nil || item.started {
		return
	}
	item.started = true
	command, err := m.commandForPane(item)
	if err != nil {
		item.err = err
		m.notice = item.title + " unavailable: " + err.Error()
		return
	}
	if m.store != nil && (item.profile == profileCodex || item.profile == profileClaude) {
		provider := profileStorageName(item.profile)
		if err := state.RegisterSessionLaunch(m.store.Path(), item.id, provider, item.cwd); err != nil {
			_ = state.AppendHookDiagnostic(m.store.Path(), provider, item.id, fmt.Errorf("register agent launch: %w", err))
		}
	}
	session, err := terminal.Start(item.id, command, 80, 24, m.events)
	if err != nil {
		item.err = err
		m.notice = item.title + " unavailable: " + err.Error()
		return
	}
	item.session = session
}

func (m *Model) commandForPane(item *pane) (terminal.Command, error) {
	if item.profile == profileShell {
		return terminal.DefaultShell(item.cwd), nil
	}
	for _, option := range m.launchOptions {
		if option.profile != item.profile {
			continue
		}
		if !option.available {
			return terminal.Command{}, fmt.Errorf("%s was not found on PATH", option.title)
		}
		command := option.command
		command.Args = append([]string(nil), command.Args...)
		command.Dir = item.cwd
		command.Env = map[string]string{
			"SPLIT_PANE_ID":  item.id,
			"SPLIT_PROVIDER": profileStorageName(item.profile),
		}
		if m.store != nil {
			command.Env["SPLIT_STATE_DB"] = m.store.Path()
		}
		if item.providerSessionID != "" {
			switch item.profile {
			case profileCodex:
				command.Args = append(command.Args, "resume", item.providerSessionID)
			case profileClaude:
				flag := "--session-id"
				if item.resumeSession {
					flag = "--resume"
				}
				command.Args = append(command.Args, flag, item.providerSessionID)
			}
		}
		return command, nil
	}
	return terminal.Command{}, errors.New("unknown launch profile")
}

func (m *Model) active() *tab {
	if len(m.tabs) == 0 {
		return nil
	}
	m.activeTab = max(0, min(m.activeTab, len(m.tabs)-1))
	return m.tabs[m.activeTab]
}

func (m *Model) activePane() *pane {
	active := m.active()
	if active == nil {
		return nil
	}
	return m.panes[active.activePane]
}

func (m *Model) activeProjectRoot() string {
	if active := m.active(); active != nil && active.rootPath != "" {
		return active.rootPath
	}
	return m.root
}

func (m *Model) effectiveSidebarWidth() int {
	if !m.sidebarVisible || m.width < 72 {
		return 0
	}
	return sidebarWidth
}

func (m *Model) workspaceRect() layout.Rect {
	left := m.effectiveSidebarWidth()
	return layout.Rect{
		X:      left,
		Y:      0,
		Width:  max(1, m.width-left),
		Height: max(1, m.height-statusHeight),
	}
}

func (m *Model) resizeActivePanes() {
	active := m.active()
	if active == nil {
		return
	}
	rects := active.root.Rects(m.workspaceRect())
	for paneID, rect := range rects {
		item := m.panes[paneID]
		if item == nil || item.session == nil {
			continue
		}
		_ = item.session.Resize(max(2, rect.Width-2), max(1, rect.Height-2))
	}
}

func (m *Model) splitActive(axis layout.Axis) {
	m.splitActiveProfile(profileShell, axis)
}

func (m *Model) splitActiveProfile(profile paneProfile, axis layout.Axis) {
	active := m.active()
	if active == nil {
		return
	}
	item := m.newProfilePane(profile)
	if item == nil {
		return
	}
	if !active.root.Split(active.activePane, item.id, axis) {
		if item.session != nil {
			item.session.Close()
		}
		delete(m.panes, item.id)
		return
	}
	active.activePane = item.id
	layout.Equalize(active.root)
	m.mode = modeNavigate
	m.focus = focusPanes
	m.notice = "Created and balanced a new " + item.title + " pane"
	m.resizeActivePanes()
	m.persist()
}

func (m *Model) newProject() {
	shell := m.newTerminalPane("PowerShell")
	title := fmt.Sprintf("project %d", m.nextProjectNumber)
	m.nextProjectNumber++
	m.appendTab(shell, title)
	m.notice = "Created a new PowerShell project"
	m.persist()
}

func (m *Model) newTab() {
	m.newTabProfile(profileShell)
}

func (m *Model) newTabProfile(profile paneProfile) {
	item := m.newProfilePane(profile)
	if item == nil {
		return
	}
	m.appendTab(item, profileTabTitle(profile, len(m.tabs)+1))
	m.notice = "Created a new " + item.title + " project"
	m.persist()
}

func (m *Model) appendTab(item *pane, title string) {
	m.tabs = append(m.tabs, &tab{
		id:         m.newID("tab"),
		title:      title,
		rootPath:   item.cwd,
		root:       layout.Leaf(item.id),
		activePane: item.id,
	})
	m.activateLastTab()
}

func (m *Model) activateLastTab() {
	m.activeTab = len(m.tabs) - 1
	m.sidebarCursor = m.activeTab
	m.mode = modeNavigate
	m.focus = focusPanes
	m.resizeActivePanes()
}

func profileTabTitle(profile paneProfile, index int) string {
	switch profile {
	case profileCodex:
		return fmt.Sprintf("codex %d", index)
	case profileClaude:
		return fmt.Sprintf("claude %d", index)
	default:
		return fmt.Sprintf("terminal %d", index)
	}
}

func (m *Model) openLauncher() {
	m.launcherOpen = true
	m.launcherSelected = 0
	for index := 1; index < len(m.launchOptions); index++ {
		if m.launchOptions[index].available {
			m.launcherSelected = index
			break
		}
	}
	m.mode = modeNavigate
	m.notice = ""
}

func (m *Model) moveLauncher(delta int) {
	if len(m.launchOptions) == 0 {
		return
	}
	m.launcherSelected = (m.launcherSelected + delta + len(m.launchOptions)) % len(m.launchOptions)
}

func (m *Model) launchSelected(axis layout.Axis, inNewTab bool) {
	if m.launcherSelected < 0 || m.launcherSelected >= len(m.launchOptions) {
		return
	}
	option := m.launchOptions[m.launcherSelected]
	if !option.available {
		m.notice = option.title + " was not found on PATH"
		return
	}
	m.launcherOpen = false
	if inNewTab {
		m.newTabProfile(option.profile)
		return
	}
	m.splitActiveProfile(option.profile, axis)
}

func (m *Model) closeActivePane() {
	active := m.active()
	if active == nil {
		return
	}
	leaves := active.root.Leaves()
	if len(leaves) == 1 {
		if len(m.tabs) == 1 {
			m.mode = modeNavigate
			m.focus = focusPanes
			m.notice = "Split keeps at least one pane open"
			return
		}
		m.closePane(active.activePane)
		m.tabs = append(m.tabs[:m.activeTab], m.tabs[m.activeTab+1:]...)
		m.activeTab = max(0, min(m.activeTab, len(m.tabs)-1))
		m.sidebarCursor = m.activeTab
		m.mode = modeNavigate
		m.ensureProjectStarted(m.active())
		m.resizeActivePanes()
		m.persist()
		return
	}

	oldPaneID := active.activePane
	root, removed := layout.Remove(active.root, oldPaneID)
	if !removed {
		return
	}
	active.root = root
	m.closePane(oldPaneID)
	layout.Equalize(active.root)
	active.activePane = active.root.Leaves()[0]
	m.mode = modeNavigate
	m.focus = focusPanes
	m.notice = "Closed and rebalanced pane"
	m.resizeActivePanes()
	m.persist()
}

func (m *Model) closePane(paneID string) {
	if item := m.panes[paneID]; item != nil && item.session != nil {
		item.session.Close()
	}
	delete(m.panes, paneID)
}

func (m *Model) switchTab(delta int) {
	if len(m.tabs) < 2 {
		return
	}
	m.activeTab = (m.activeTab + delta + len(m.tabs)) % len(m.tabs)
	m.sidebarCursor = m.activeTab
	m.focus = focusPanes
	m.ensureProjectStarted(m.active())
	m.resizeActivePanes()
	m.persist()
}

func (m *Model) movePaneFocus(direction layout.Direction) {
	active := m.active()
	if active == nil {
		return
	}
	next := layout.Neighbor(active.root, active.activePane, direction, m.workspaceRect())
	if next != "" {
		active.activePane = next
		m.persist()
	}
}

func (m *Model) swapActivePane(direction layout.Direction) {
	active := m.active()
	if active == nil {
		return
	}
	neighbor := layout.Neighbor(active.root, active.activePane, direction, m.workspaceRect())
	if neighbor == "" {
		m.mode = modeNavigate
		m.notice = "There is no pane in that direction"
		return
	}
	if !layout.Swap(active.root, active.activePane, neighbor) {
		m.mode = modeNavigate
		m.notice = "Could not move pane"
		return
	}
	m.mode = modeNavigate
	m.focus = focusPanes
	m.notice = "Moved pane"
	m.resizeActivePanes()
	m.persist()
}

func (m *Model) balanceActiveLayout() {
	active := m.active()
	if active == nil {
		return
	}
	layout.Equalize(active.root)
	m.mode = modeNavigate
	m.focus = focusPanes
	m.notice = "Balanced panes"
	m.resizeActivePanes()
	m.persist()
}

func (m *Model) selectTab(index int) {
	if index < 0 || index >= len(m.tabs) {
		return
	}
	m.activeTab = index
	m.sidebarCursor = index
	m.focus = focusPanes
	m.mode = modeNavigate
	m.ensureProjectStarted(m.active())
	m.resizeActivePanes()
	m.persist()
}

func (m *Model) setNoticeFromEvents(events []terminal.Event) {
	for _, event := range events {
		switch event.Kind {
		case terminal.ProcessFailed:
			m.notice = "Terminal failed: " + event.Err.Error()
		case terminal.ProcessExited:
			if event.Err != nil {
				m.notice = "Terminal process exited"
			}
		}
	}
}

func waitForTerminalEvents(events <-chan terminal.Event) tea.Cmd {
	return func() tea.Msg {
		first := <-events
		batch := []terminal.Event{first}
		timer := time.NewTimer(time.Second / 60)
		defer timer.Stop()

		for {
			select {
			case event := <-events:
				batch = append(batch, event)
			case <-timer.C:
				return terminalBatchMsg{events: batch}
			}
		}
	}
}

func (m *Model) projectName() string {
	name := filepath.Base(m.root)
	if name == "." || name == string(filepath.Separator) {
		return m.root
	}
	return name
}
