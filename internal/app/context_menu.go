package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"split/internal/layout"
	"split/internal/terminal"
)

type paneContextMenuAction uint8

const (
	paneMenuNoop paneContextMenuAction = iota
	paneMenuSplitRight
	paneMenuSplitBelow
	paneMenuNewProject
	paneMenuMove
	paneMenuBalance
	paneMenuClose
)

type paneContextMenuItem struct {
	label     string
	hint      string
	action    paneContextMenuAction
	direction layout.Direction
	enabled   bool
	separator bool
}

type paneContextMenuState struct {
	open         bool
	x            int
	y            int
	selected     int
	targetPane   string
	previousMode mode
}

type paneContextMenuGeometry struct {
	x      int
	y      int
	width  int
	height int
}

func (m *Model) openPaneContextMenu(x, y int, paneID string) {
	active := m.active()
	if active == nil || !active.root.ContainsPane(paneID) {
		return
	}
	active.activePane = paneID
	m.contextMenu = paneContextMenuState{
		open:         true,
		x:            x,
		y:            y,
		targetPane:   paneID,
		previousMode: m.mode,
	}
	m.contextMenu.selected = firstContextMenuSelection(m.paneContextMenuItems())
	m.focus = focusPanes
	m.mode = modeNavigate
	m.notice = ""
	m.persist()
}

func (m *Model) closePaneContextMenu(restoreMode bool) {
	previous := m.contextMenu.previousMode
	targetPane := m.contextMenu.targetPane
	m.contextMenu = paneContextMenuState{}
	if !restoreMode || previous != modeTerminal {
		m.mode = modeNavigate
		return
	}
	active := m.active()
	item := m.panes[targetPane]
	if active == nil || active.activePane != targetPane || item == nil || item.session == nil {
		m.mode = modeNavigate
		return
	}
	state, _ := item.session.State()
	if state == terminal.Running {
		m.mode = modeTerminal
	} else {
		m.mode = modeNavigate
	}
}

func (m *Model) paneContextMenuItems() []paneContextMenuItem {
	active := m.active()
	paneCount := 0
	canClose := false
	if active != nil {
		paneCount = len(active.root.Leaves())
		canClose = paneCount > 1 || len(m.tabs) > 1
	}
	return []paneContextMenuItem{
		{label: "Split right", hint: "\u2192", action: paneMenuSplitRight, enabled: active != nil},
		{label: "Split below", hint: "\u2193", action: paneMenuSplitBelow, enabled: active != nil},
		{label: "Open in new project", hint: "+", action: paneMenuNewProject, enabled: true},
		{separator: true},
		{label: "Move left", hint: "\u2190", action: paneMenuMove, direction: layout.Left, enabled: m.canMoveActivePane(layout.Left)},
		{label: "Move right", hint: "\u2192", action: paneMenuMove, direction: layout.Right, enabled: m.canMoveActivePane(layout.Right)},
		{label: "Move up", hint: "\u2191", action: paneMenuMove, direction: layout.Up, enabled: m.canMoveActivePane(layout.Up)},
		{label: "Move down", hint: "\u2193", action: paneMenuMove, direction: layout.Down, enabled: m.canMoveActivePane(layout.Down)},
		{separator: true},
		{label: "Balance panes", hint: "=", action: paneMenuBalance, enabled: paneCount > 1},
		{label: "Close pane", hint: "x", action: paneMenuClose, enabled: canClose},
	}
}

func (m *Model) canMoveActivePane(direction layout.Direction) bool {
	active := m.active()
	if active == nil {
		return false
	}
	return layout.Neighbor(active.root, active.activePane, direction, m.workspaceRect()) != ""
}

func firstContextMenuSelection(items []paneContextMenuItem) int {
	for index, item := range items {
		if item.enabled && !item.separator {
			return index
		}
	}
	return -1
}

func (m *Model) movePaneContextMenuSelection(delta int) {
	items := m.paneContextMenuItems()
	if len(items) == 0 {
		m.contextMenu.selected = -1
		return
	}
	selected := m.contextMenu.selected
	for range len(items) {
		selected = (selected + delta + len(items)) % len(items)
		if items[selected].enabled && !items[selected].separator {
			m.contextMenu.selected = selected
			return
		}
	}
}

func (m *Model) activatePaneContextMenuItem(index int) {
	items := m.paneContextMenuItems()
	if index < 0 || index >= len(items) || !items[index].enabled || items[index].separator {
		return
	}
	item := items[index]
	m.contextMenu.selected = index
	m.closePaneContextMenu(false)
	switch item.action {
	case paneMenuSplitRight:
		m.splitActive(layout.Columns)
	case paneMenuSplitBelow:
		m.splitActive(layout.Rows)
	case paneMenuNewProject:
		m.newProject()
	case paneMenuMove:
		m.swapActivePane(item.direction)
	case paneMenuBalance:
		m.balanceActiveLayout()
	case paneMenuClose:
		m.closeActivePane()
	}
}

func (m *Model) handlePaneContextMenuKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "esc", "escape", "q", "left", "h":
		m.closePaneContextMenu(true)
	case "up", "k":
		m.movePaneContextMenuSelection(-1)
	case "down", "j":
		m.movePaneContextMenuSelection(1)
	case "enter", "right", "l":
		m.activatePaneContextMenuItem(m.contextMenu.selected)
	}
	return m, nil
}

func (m *Model) handlePaneContextMenuMotion(mouse tea.Mouse) {
	if index, ok := m.paneContextMenuItemAt(mouse.X, mouse.Y); ok {
		m.contextMenu.selected = index
	}
}

func (m *Model) handlePaneContextMenuClick(mouse tea.Mouse) {
	if mouse.Button == tea.MouseRight {
		m.closePaneContextMenu(false)
		m.handleMouseClick(mouse)
		return
	}
	if mouse.Button != tea.MouseLeft {
		return
	}
	if index, ok := m.paneContextMenuItemAt(mouse.X, mouse.Y); ok {
		m.activatePaneContextMenuItem(index)
		return
	}
	m.closePaneContextMenu(true)
	m.handleMouseClick(mouse)
}

func (m *Model) paneContextMenuItemAt(x, y int) (int, bool) {
	geometry := m.paneContextMenuGeometry()
	if x <= geometry.x || x >= geometry.x+geometry.width-1 ||
		y <= geometry.y || y >= geometry.y+geometry.height-1 {
		return -1, false
	}
	index := y - geometry.y - 1
	items := m.paneContextMenuItems()
	if index < 0 || index >= len(items) || items[index].separator || !items[index].enabled {
		return -1, false
	}
	return index, true
}

func (m *Model) paneContextMenuGeometry() paneContextMenuGeometry {
	items := m.paneContextMenuItems()
	width := 28
	for _, item := range items {
		width = max(width, ansi.StringWidth(item.label)+ansi.StringWidth(item.hint)+5)
	}
	width = min(width, m.width)
	height := min(len(items)+2, m.height)
	x := max(0, min(m.contextMenu.x, m.width-width))
	y := max(0, min(m.contextMenu.y, m.height-height))
	return paneContextMenuGeometry{x: x, y: y, width: width, height: height}
}

func (m *Model) renderPaneContextMenuOverlay(base string) string {
	geometry := m.paneContextMenuGeometry()
	items := m.paneContextMenuItems()
	innerWidth := max(0, geometry.width-2)
	border := lipgloss.NewStyle().Foreground(palette.accent)
	title := " Pane "
	title = ansi.Truncate(title, innerWidth, "")
	top := border.Render("\u250c" + title + strings.Repeat("\u2500", max(0, innerWidth-ansi.StringWidth(title))) + "\u2510")

	lines := make([]string, 0, geometry.height)
	lines = append(lines, top)
	for index, item := range items {
		if item.separator {
			lines = append(lines, border.Render("\u251c"+strings.Repeat("\u2500", innerWidth)+"\u2524"))
			continue
		}
		label := " " + item.label
		gap := max(1, innerWidth-ansi.StringWidth(label)-ansi.StringWidth(item.hint)-1)
		row := fitLine(label+strings.Repeat(" ", gap)+item.hint+" ", innerWidth)
		switch {
		case !item.enabled:
			row = lipgloss.NewStyle().Foreground(palette.muted).Faint(true).Render(row)
		case index == m.contextMenu.selected:
			row = lipgloss.NewStyle().Foreground(palette.background).Background(palette.accent).Bold(true).Render(row)
		default:
			row = styles.text.Render(row)
		}
		lines = append(lines, border.Render("\u2502")+row+border.Render("\u2502"))
	}
	lines = append(lines, border.Render("\u2514"+strings.Repeat("\u2500", innerWidth)+"\u2518"))
	menu := fitBlock(strings.Join(lines, "\n"), geometry.width, geometry.height)

	composed := lipgloss.NewCompositor(
		lipgloss.NewLayer(fitBlock(base, m.width, m.height)).X(0).Y(0).Z(0),
		lipgloss.NewLayer(menu).X(geometry.x).Y(geometry.y).Z(2),
	).Render()
	return fitBlock(composed, m.width, m.height)
}
