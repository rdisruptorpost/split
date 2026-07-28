package app

import (
	tea "charm.land/bubbletea/v2"

	"split/internal/layout"
	"split/internal/terminal"
)

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, message.Width)
		m.height = max(1, message.Height)
		m.resizeActivePanes()
		return m, nil

	case terminalBatchMsg:
		m.setNoticeFromEvents(message.events)
		return m, waitForTerminalEvents(m.events)

	case tea.PasteMsg:
		if m.mode == modeTerminal && !m.launcherOpen && !m.contextMenu.open {
			if item := m.activePane(); item != nil && item.session != nil {
				item.session.Paste(message.Content)
			}
		}
		return m, nil

	case tea.MouseClickMsg:
		if m.contextMenu.open {
			m.handlePaneContextMenuClick(message.Mouse())
		} else if !m.launcherOpen {
			m.handleMouseClick(message.Mouse())
		}
		return m, nil

	case tea.MouseMotionMsg:
		if m.contextMenu.open {
			m.handlePaneContextMenuMotion(message.Mouse())
		}
		return m, nil

	case tea.KeyPressMsg:
		if m.contextMenu.open {
			return m.handlePaneContextMenuKey(message)
		}
		if m.launcherOpen {
			return m.handleLauncherKey(message)
		}
		return m.handleKey(message)
	}

	return m, nil
}

func (m *Model) handleKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeTerminal:
		if message.String() == "ctrl+b" {
			m.modeBeforePrefix = modeTerminal
			m.mode = modePrefix
			return m, nil
		}
		if item := m.activePane(); item != nil && item.session != nil {
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
	case "a":
		m.openLauncher()
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
	case "q":
		return m, tea.Quit
	default:
		m.mode = m.modeBeforePrefix
	}
	return m, nil
}

func (m *Model) handleLauncherKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "esc", "escape", "q":
		m.launcherOpen = false
		m.mode = modeNavigate
	case "up", "k":
		m.moveLauncher(-1)
	case "down", "j":
		m.moveLauncher(1)
	case "enter", "v":
		m.launchSelected(layout.Columns, false)
	case "s":
		m.launchSelected(layout.Rows, false)
	case "t":
		m.launchSelected(layout.Columns, true)
	}
	return m, nil
}

func (m *Model) handleMouseClick(mouse tea.Mouse) {
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
				m.openPaneContextMenu(mouse.X, mouse.Y, paneID)
				return
			}
		}
		return
	}
	if mouse.Button != tea.MouseLeft {
		return
	}

	sidebar := m.effectiveSidebarWidth()
	if sidebar > 0 && mouse.X < sidebar {
		index := mouse.Y - sidebarProjectStart
		switch {
		case index >= 0 && index < len(m.tabs):
			m.sidebarCursor = index
			m.selectTab(index)
		case index == len(m.tabs):
			m.newProject()
		}
		return
	}

	if !m.workspaceRect().Contains(mouse.X, mouse.Y) {
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
				return
			}
			m.notice = "This terminal is not running"
		}
		m.mode = modeNavigate
		return
	}
}
