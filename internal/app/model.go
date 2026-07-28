package app

import (
	"fmt"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"split/internal/agent"
	"split/internal/layout"
	"split/internal/state"
	"split/internal/terminal"
)

const (
	defaultWidth        = 120
	defaultHeight       = 36
	sidebarWidth        = 24
	sidebarProjectStart = 5
	statusHeight        = 1
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

type pane struct {
	id      string
	title   string
	kind    paneKind
	cwd     string
	started bool
	session *terminal.Session
	err     error
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
	agents            map[string]agent.State
	agentTracker      *agent.Tracker
	nextID            int
	nextProjectNumber int

	events chan terminal.Event
	notice string

	contextMenu     paneContextMenuState
	detachRequested bool

	store *state.Store
}

type terminalBatchMsg struct {
	events []terminal.Event
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
		agents:            make(map[string]agent.State),
		agentTracker:      agent.NewTracker(),
		events:            make(chan terminal.Event, 128),
		nextProjectNumber: 2,
	}

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
	return m.newPane(title)
}

func (m *Model) newPane(title string) *pane {
	item := &pane{
		id:    m.newID("pane"),
		title: title,
		kind:  paneTerminal,
		cwd:   m.activeProjectRoot(),
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
	session, err := terminal.Start(item.id, terminal.DefaultShell(item.cwd), 80, 24, m.events)
	if err != nil {
		item.err = err
		m.notice = item.title + " unavailable: " + err.Error()
		return
	}
	item.session = session
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
	active := m.active()
	if active == nil {
		return
	}
	item := m.newTerminalPane("PowerShell")
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
	m.notice = "Created and balanced a new PowerShell pane"
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
	item := m.newTerminalPane("PowerShell")
	m.appendTab(item, fmt.Sprintf("terminal %d", len(m.tabs)+1))
	m.notice = "Created a new PowerShell project"
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

// TerminalEvents exposes the process update stream to the detached runtime.
func (m *Model) TerminalEvents() <-chan terminal.Event {
	return m.events
}

// ApplyTerminalEvents updates notices after the runtime drains terminal output.
func (m *Model) ApplyTerminalEvents(events []terminal.Event) {
	m.setNoticeFromEvents(events)
}

// RefreshAgents discovers Codex and Claude descendants in every live pane and
// updates their terminal-derived state. It returns true when the visible state
// changed and a new client frame is needed.
func (m *Model) RefreshAgents(now time.Time) bool {
	targets := make([]agent.Target, 0, len(m.panes))
	for paneID, item := range m.panes {
		if item == nil || item.session == nil {
			continue
		}
		terminalState, _ := item.session.State()
		targets = append(targets, agent.Target{
			PaneID:     paneID,
			RootPID:    item.session.ProcessID(),
			Screen:     item.session.Render(),
			Title:      item.session.Title(),
			LastOutput: item.session.LastActivity(),
			TerminalUp: terminalState == terminal.Running,
		})
	}

	next := m.agentTracker.Refresh(targets, now)
	changed := !sameAgentStates(m.agents, next)
	m.agents = next
	return changed
}

// markAgentSubmitted immediately reflects an Enter key sent to a recognized
// agent while keeping the tracker in sync for the next process scan.
func (m *Model) markAgentSubmitted(paneID string, now time.Time) bool {
	current, exists := m.agents[paneID]
	if !exists || current.Status == agent.StatusExited {
		return false
	}
	if m.agentTracker != nil {
		m.agentTracker.MarkSubmitted(paneID, now)
	}
	if current.Status != agent.StatusWorking {
		current.Since = now
	}
	current.Status = agent.StatusWorking
	m.agents[paneID] = current
	return true
}

// markAgentInterrupted immediately shows an interrupted turn after Esc. The
// tracker holds this state briefly so the next screen scan cannot turn it into
// a successful completion tick.
func (m *Model) markAgentInterrupted(paneID string, now time.Time) bool {
	current, exists := m.agents[paneID]
	if !exists || (current.Status != agent.StatusWorking && current.Status != agent.StatusLoading) {
		return false
	}
	if m.agentTracker != nil {
		m.agentTracker.MarkInterrupted(paneID, now)
	}
	current.Status = agent.StatusInterrupted
	current.Since = now
	m.agents[paneID] = current
	return true
}

// HasAnimatingAgents reports whether the connected client needs quicker frames
// for a loading or working spinner.
func (m *Model) HasAnimatingAgents() bool {
	for _, current := range m.agents {
		if current.Status == agent.StatusLoading || current.Status == agent.StatusWorking {
			return true
		}
	}
	return false
}

func sameAgentStates(left, right map[string]agent.State) bool {
	if len(left) != len(right) {
		return false
	}
	for paneID, current := range left {
		if other, exists := right[paneID]; !exists || current != other {
			return false
		}
	}
	return true
}

// TakeDetachRequest reports and clears a client detach request.
func (m *Model) TakeDetachRequest() bool {
	requested := m.detachRequested
	m.detachRequested = false
	return requested
}

// ClientDetached closes transient UI state without closing any terminal.
func (m *Model) ClientDetached() {
	m.contextMenu = paneContextMenuState{}
	m.mode = modeNavigate
	m.modeBeforePrefix = modeNavigate
	m.focus = focusPanes
	m.detachRequested = false
	m.persist()
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
