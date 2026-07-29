package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"split/internal/layout"
	"split/internal/terminal"
)

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.clearTerminalSelection()
		m.width = max(1, message.Width)
		m.height = max(1, message.Height)
		m.resizeActivePanes()
		return m, nil

	case terminalBatchMsg:
		m.setNoticeFromEvents(message.events)
		return m, waitForTerminalEvents(m.events)

	case tea.PasteMsg:
		if m.renameDialog.open {
			m.insertProjectRenameText(message.Content)
		} else if m.mode == modeTerminal && !m.contextMenu.open {
			if item := m.activePane(); item != nil && item.session != nil {
				m.clearTerminalSelection()
				item.session.Paste(message.Content)
			}
		}
		return m, nil

	case tea.MouseClickMsg:
		if m.renameDialog.open {
			m.handleProjectRenameClick(message.Mouse())
		} else if m.projectMenu.open {
			m.handleProjectContextMenuClick(message.Mouse())
		} else if m.contextMenu.open {
			m.handlePaneContextMenuClick(message.Mouse())
		} else {
			m.handleMouseClick(message.Mouse())
		}
		return m, nil

	case tea.MouseReleaseMsg:
		if !m.renameDialog.open && !m.projectMenu.open && !m.contextMenu.open {
			m.handleTerminalSelectionRelease(message.Mouse())
		}
		return m, nil

	case tea.MouseWheelMsg:
		if !m.renameDialog.open && !m.projectMenu.open && !m.contextMenu.open {
			m.handleMouseWheel(message.Mouse())
		}
		return m, nil

	case tea.MouseMotionMsg:
		if m.renameDialog.open {
			m.handleProjectRenameMotion(message.Mouse())
		} else if m.projectMenu.open {
			m.handleProjectContextMenuMotion(message.Mouse())
		} else if m.contextMenu.open {
			m.handlePaneContextMenuMotion(message.Mouse())
		} else {
			m.handleTerminalSelectionMotion(message.Mouse())
		}
		return m, nil

	case tea.KeyPressMsg:
		if m.renameDialog.open {
			return m.handleProjectRenameKey(message)
		}
		if m.projectMenu.open {
			return m.handleProjectContextMenuKey(message)
		}
		if m.contextMenu.open {
			return m.handlePaneContextMenuKey(message)
		}
		return m.handleKey(message)
	}

	return m, nil
}

func (m *Model) handleKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeTerminal:
		if message.String() == "ctrl+c" {
			if item := m.activePane(); item != nil && m.copyPaneSelection(item.id) {
				return m, nil
			}
		}
		if message.String() == "ctrl+b" {
			m.clearTerminalSelection()
			m.modeBeforePrefix = modeTerminal
			m.mode = modePrefix
			return m, nil
		}
		if item := m.activePane(); item != nil && item.session != nil {
			m.clearTerminalSelection()
			switch message.String() {
			case "enter":
				m.markAgentSubmitted(item.id, time.Now())
			case "esc", "escape":
				m.markAgentInterrupted(item.id, time.Now())
			}
			item.session.SendKey(message)
		}
		return m, nil

	case modePrefix:
		return m.handlePrefixKey(message)

	default:
		return m.handleNavigationKey(message)
	}
}

func (m *Model) handleNavigationKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "ctrl+c", "q":
		m.detachRequested = true
		return m, tea.Quit
	case "ctrl+b":
		m.modeBeforePrefix = modeNavigate
		m.mode = modePrefix
	case "tab":
		if m.effectiveSidebarWidth() == 0 {
			m.focus = focusPanes
		} else if m.focus == focusPanes {
			m.focus = focusSidebar
			m.sidebarCursor = m.activeTab
		} else {
			m.focus = focusPanes
		}
	case "enter":
		if m.focus == focusSidebar {
			if m.sidebarCursor == len(m.tabs) {
				m.newProject()
			} else {
				m.selectTab(m.sidebarCursor)
			}
			return m, nil
		}
		item := m.activePane()
		if item != nil && item.session != nil {
			m.mode = modeTerminal
			m.notice = ""
		} else {
			m.notice = "This pane is informational; focus a terminal to type"
		}
	case "[", "shift+tab":
		m.switchTab(-1)
	case "]":
		m.switchTab(1)
	default:
		m.handleDirectionalNavigation(message.String())
	}
	return m, nil
}

func (m *Model) handleDirectionalNavigation(key string) {
	if m.focus == focusSidebar {
		switch key {
		case "up", "k":
			m.sidebarCursor = max(0, m.sidebarCursor-1)
		case "down", "j":
			m.sidebarCursor = min(len(m.tabs), m.sidebarCursor+1)
		}
		return
	}

	switch key {
	case "left", "h":
		m.movePaneFocus(layout.Left)
	case "right", "l":
		m.movePaneFocus(layout.Right)
	case "up", "k":
		m.movePaneFocus(layout.Up)
	case "down", "j":
		m.movePaneFocus(layout.Down)
	}
}

func (m *Model) handlePrefixKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := message.String()
	switch key {
	case "esc", "escape":
		m.mode = m.modeBeforePrefix
	case "ctrl+b", "b":
		if item := m.activePane(); item != nil && item.session != nil {
			item.session.SendKey(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
		}
		m.mode = modeTerminal
	case "n":
		m.mode = modeNavigate
		m.focus = focusPanes
	case "c":
		m.newTab()
	case "%", "v":
		m.splitActive(layout.Columns)
	case "\"", "s":
		m.splitActive(layout.Rows)
	case "x":
		m.closeActivePane()
	case "h", "left":
		m.swapActivePane(layout.Left)
	case "l", "right":
		m.swapActivePane(layout.Right)
	case "k", "up":
		m.swapActivePane(layout.Up)
	case "j", "down":
		m.swapActivePane(layout.Down)
	case "=", "e":
		m.balanceActiveLayout()
	case "[":
		m.switchTab(-1)
		m.mode = modeNavigate
	case "]":
		m.switchTab(1)
		m.mode = modeNavigate
	case "w":
		m.sidebarVisible = !m.sidebarVisible
		if !m.sidebarVisible {
			m.focus = focusPanes
		}
		m.mode = modeNavigate
		m.resizeActivePanes()
		m.persist()
	case "q":
		m.detachRequested = true
		return m, tea.Quit
	default:
		m.mode = m.modeBeforePrefix
	}
	return m, nil
}

func (m *Model) handleMouseClick(mouse tea.Mouse) {
	sidebar := m.effectiveSidebarWidth()
	if sidebar > 0 && mouse.X < sidebar {
		if mouse.Button != tea.MouseLeft && mouse.Button != tea.MouseRight {
			return
		}
		m.clearTerminalSelection()
		m.focusSidebarNavigation(m.activeTab)
		m.notice = ""
		row, hasRow := m.sidebarRowAt(mouse.Y)

		if mouse.Button == tea.MouseRight {
			if hasRow && row.kind == sidebarProjectRow {
				m.openProjectContextMenu(mouse.X, mouse.Y, row.projectIndex)
			}
			return
		}
		if !hasRow {
			return
		}

		switch row.kind {
		case sidebarProjectRow:
			m.selectTab(row.projectIndex)
			m.focusSidebarNavigation(row.projectIndex)
		case sidebarAgentRow:
			m.selectTab(row.projectIndex)
			if active := m.active(); active != nil {
				active.activePane = row.paneID
			}
			m.focusSidebarNavigation(row.projectIndex)
			m.persist()
		case sidebarNewProjectRow:
			m.newProject()
			m.focusSidebarNavigation(m.activeTab)
		}
		return
	}

	if mouse.Button == tea.MouseRight {
		if !m.workspaceRect().Contains(mouse.X, mouse.Y) {
			return
		}
		active := m.active()
		if active == nil {
			return
		}
		for paneID, rect := range active.root.Rects(m.workspaceRect()) {
			if rect.Contains(mouse.X, mouse.Y) {
				if m.copyPaneSelection(paneID) {
					return
				}
				m.clearTerminalSelection()
				m.openPaneContextMenu(mouse.X, mouse.Y, paneID)
				return
			}
		}
		m.clearTerminalSelection()
		return
	}
	if mouse.Button != tea.MouseLeft {
		return
	}

	if !m.workspaceRect().Contains(mouse.X, mouse.Y) {
		m.clearTerminalSelection()
		return
	}
	active := m.active()
	if active == nil {
		return
	}
	for paneID, rect := range active.root.Rects(m.workspaceRect()) {
		if !rect.Contains(mouse.X, mouse.Y) {
			continue
		}
		active.activePane = paneID
		m.focus = focusPanes
		m.notice = ""

		item := m.panes[paneID]
		if item != nil && item.session != nil {
			state, _ := item.session.State()
			if state == terminal.Running {
				m.mode = modeTerminal
				m.beginTerminalSelection(paneID, rect, mouse)
				m.persist()
				return
			}
			m.notice = "This terminal is not running"
		}
		m.clearTerminalSelection()
		m.mode = modeNavigate
		m.persist()
		return
	}
	m.clearTerminalSelection()
}

const terminalWheelLines = 3

func (m *Model) handleMouseWheel(mouse tea.Mouse) {
	if mouse.Button != tea.MouseWheelUp && mouse.Button != tea.MouseWheelDown {
		return
	}
	if mouse.X < m.effectiveSidebarWidth() || !m.workspaceRect().Contains(mouse.X, mouse.Y) {
		return
	}
	m.clearTerminalSelection()
	active := m.active()
	if active == nil {
		return
	}
	for paneID, rect := range active.root.Rects(m.workspaceRect()) {
		if !rect.Contains(mouse.X, mouse.Y) {
			continue
		}
		item := m.panes[paneID]
		if item == nil || item.session == nil {
			return
		}
		lines := terminalWheelLines
		if mouse.Button == tea.MouseWheelDown {
			lines = -lines
		}
		item.session.HandleWheel(
			mouse.X-rect.X-1,
			mouse.Y-rect.Y-1,
			mouse.Button,
			mouse.Mod,
			lines,
		)
		return
	}
}
