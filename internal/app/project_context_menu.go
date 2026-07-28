package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type projectContextMenuAction uint8

const (
	projectMenuNoop projectContextMenuAction = iota
	projectMenuRename
	projectMenuClose
)

type projectContextMenuItem struct {
	label   string
	hint    string
	action  projectContextMenuAction
	enabled bool
}

type projectContextMenuState struct {
	open          bool
	x             int
	y             int
	selected      int
	targetProject int
}

type projectContextMenuGeometry struct {
	x      int
	y      int
	width  int
	height int
}

func (m *Model) openProjectContextMenu(x, y, projectIndex int) {
	if projectIndex < 0 || projectIndex >= len(m.tabs) {
		return
	}
	if m.contextMenu.open {
		m.closePaneContextMenu(false)
	}
	m.focusSidebarNavigation(projectIndex)
	m.projectMenu = projectContextMenuState{
		open:          true,
		x:             x,
		y:             y,
		targetProject: projectIndex,
	}
	m.projectMenu.selected = firstProjectContextMenuSelection(m.projectContextMenuItems())
	m.notice = ""
}

func (m *Model) closeProjectContextMenu() {
	m.projectMenu = projectContextMenuState{}
	m.mode = modeNavigate
	m.focus = focusSidebar
}

func (m *Model) projectContextMenuItems() []projectContextMenuItem {
	validTarget := m.projectMenu.targetProject >= 0 &&
		m.projectMenu.targetProject < len(m.tabs)
	return []projectContextMenuItem{
		{label: "Rename project", hint: "r", action: projectMenuRename, enabled: validTarget},
		{label: "Close project", hint: "x", action: projectMenuClose, enabled: validTarget && len(m.tabs) > 1},
	}
}

func firstProjectContextMenuSelection(items []projectContextMenuItem) int {
	for index, item := range items {
		if item.enabled {
			return index
		}
	}
	return -1
}

func (m *Model) moveProjectContextMenuSelection(delta int) {
	items := m.projectContextMenuItems()
	if len(items) == 0 {
		m.projectMenu.selected = -1
		return
	}
	selected := m.projectMenu.selected
	for range len(items) {
		selected = (selected + delta + len(items)) % len(items)
		if items[selected].enabled {
			m.projectMenu.selected = selected
			return
		}
	}
}

func (m *Model) activateProjectContextMenuItem(index int) {
	items := m.projectContextMenuItems()
	if index < 0 || index >= len(items) || !items[index].enabled {
		return
	}
	targetProject := m.projectMenu.targetProject
	action := items[index].action
	m.closeProjectContextMenu()
	switch action {
	case projectMenuRename:
		m.openProjectRenameDialog(targetProject)
	case projectMenuClose:
		m.closeProject(targetProject)
	}
}

func (m *Model) handleProjectContextMenuKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "esc", "escape", "q", "left", "h":
		m.closeProjectContextMenu()
	case "up", "k":
		m.moveProjectContextMenuSelection(-1)
	case "down", "j":
		m.moveProjectContextMenuSelection(1)
	case "enter", "right", "l":
		m.activateProjectContextMenuItem(m.projectMenu.selected)
	case "r":
		m.activateProjectContextMenuItem(0)
	case "x":
		m.activateProjectContextMenuItem(1)
	}
	return m, nil
}

func (m *Model) handleProjectContextMenuMotion(mouse tea.Mouse) {
	if index, ok := m.projectContextMenuItemAt(mouse.X, mouse.Y); ok {
		m.projectMenu.selected = index
	}
}

func (m *Model) handleProjectContextMenuClick(mouse tea.Mouse) {
	if mouse.Button == tea.MouseRight {
		m.closeProjectContextMenu()
		m.handleMouseClick(mouse)
		return
	}
	if mouse.Button != tea.MouseLeft {
		return
	}
	if index, ok := m.projectContextMenuItemAt(mouse.X, mouse.Y); ok {
		m.activateProjectContextMenuItem(index)
		return
	}
	m.closeProjectContextMenu()
	m.handleMouseClick(mouse)
}

func (m *Model) projectContextMenuItemAt(x, y int) (int, bool) {
	geometry := m.projectContextMenuGeometry()
	if x <= geometry.x || x >= geometry.x+geometry.width-1 ||
		y <= geometry.y || y >= geometry.y+geometry.height-1 {
		return -1, false
	}
	index := y - geometry.y - 1
	items := m.projectContextMenuItems()
	if index < 0 || index >= len(items) || !items[index].enabled {
		return -1, false
	}
	return index, true
}

func (m *Model) projectContextMenuGeometry() projectContextMenuGeometry {
	items := m.projectContextMenuItems()
	width := 24
	for _, item := range items {
		width = max(width, ansi.StringWidth(item.label)+ansi.StringWidth(item.hint)+5)
	}
	width = min(width, m.width)
	height := min(len(items)+2, m.height)
	x := max(0, min(m.projectMenu.x, m.width-width))
	y := max(0, min(m.projectMenu.y, m.height-height))
	return projectContextMenuGeometry{x: x, y: y, width: width, height: height}
}

func (m *Model) renderProjectContextMenuOverlay(base string) string {
	geometry := m.projectContextMenuGeometry()
	items := m.projectContextMenuItems()
	innerWidth := max(0, geometry.width-2)
	border := lipgloss.NewStyle().Foreground(palette.accent)
	title := ansi.Truncate(" Project ", innerWidth, "")
	top := border.Render(
		"\u250c" + title +
			strings.Repeat("\u2500", max(0, innerWidth-ansi.StringWidth(title))) +
			"\u2510",
	)

	lines := make([]string, 0, geometry.height)
	lines = append(lines, top)
	for index, item := range items {
		label := " " + item.label
		gap := max(1, innerWidth-ansi.StringWidth(label)-ansi.StringWidth(item.hint)-1)
		row := fitLine(label+strings.Repeat(" ", gap)+item.hint+" ", innerWidth)
		switch {
		case !item.enabled:
			row = lipgloss.NewStyle().
				Foreground(palette.muted).
				Faint(true).
				Render(row)
		case index == m.projectMenu.selected:
			row = lipgloss.NewStyle().
				Foreground(palette.background).
				Background(palette.accent).
				Bold(true).
				Render(row)
		default:
			row = styles.text.Render(row)
		}
		lines = append(lines, border.Render("\u2502")+row+border.Render("\u2502"))
	}
	lines = append(
		lines,
		border.Render("\u2514"+strings.Repeat("\u2500", innerWidth)+"\u2518"),
	)
	menu := fitBlock(strings.Join(lines, "\n"), geometry.width, geometry.height)

	composed := lipgloss.NewCompositor(
		lipgloss.NewLayer(fitBlock(base, m.width, m.height)).X(0).Y(0).Z(0),
		lipgloss.NewLayer(menu).X(geometry.x).Y(geometry.y).Z(2),
	).Render()
	return fitBlock(composed, m.width, m.height)
}
