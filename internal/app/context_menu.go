package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"split/internal/layout"
	"split/internal/terminal"
)

type paneContextMenuKind uint8

const (
	paneMenuRoot paneContextMenuKind = iota
	paneMenuSplitRight
	paneMenuSplitBelow
	paneMenuNewTab
)

type paneContextMenuAction uint8

const (
	paneMenuNoop paneContextMenuAction = iota
	paneMenuOpenSplitRight
	paneMenuOpenSplitBelow
	paneMenuOpenNewTab
	paneMenuLaunchProfile
	paneMenuMove
	paneMenuBalance
	paneMenuClose
	paneMenuBack
)

type paneContextMenuItem struct {
	label     string
	hint      string
	action    paneContextMenuAction
	profile   paneProfile
	direction layout.Direction
	enabled   bool
	separator bool
}

type paneContextMenuState struct {
	open         bool
	x            int
	y            int
	kind         paneContextMenuKind
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
		kind:         paneMenuRoot,
		targetPane:   paneID,
		previousMode: m.mode,
	}
	m.contextMenu.selected = firstContextMenuSelection(m.paneContextMenuItems())
	m.focus = focusPanes
	m.mode = modeNavigate
	m.notice = ""
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
	if m.contextMenu.kind != paneMenuRoot {
		items := make([]paneContextMenuItem, 0, len(m.launchOptions)+2)
		for _, option := range m.launchOptions {
			items = append(items, paneContextMenuItem{
				label:   option.title,
				hint:    availabilityHint(option.available),
				action:  paneMenuLaunchProfile,
				profile: option.profile,
				enabled: option.available,
			})
		}
		items = append(items,
			paneContextMenuItem{separator: true},
			paneContextMenuItem{label: "Back", hint: "←", action: paneMenuBack, enabled: true},
		)
		return items
	}

	active := m.active()
	paneCount := 0
	canClose := false
	if active != nil {
		paneCount = len(active.root.Leaves())
		canClose = paneCount > 1 || len(m.tabs) > 1
	}
	return []paneContextMenuItem{
		{label: "Split right…", hint: "›", action: paneMenuOpenSplitRight, enabled: active != nil},
		{label: "Split below…", hint: "›", action: paneMenuOpenSplitBelow, enabled: active != nil},
		{label: "Open in new project…", hint: "›", action: paneMenuOpenNewTab, enabled: true},
		{separator: true},
		{label: "Move left", hint: "←", action: paneMenuMove, direction: layout.Left, enabled: m.canMoveActivePane(layout.Left)},
		{label: "Move right", hint: "→", action: paneMenuMove, direction: layout.Right, enabled: m.canMoveActivePane(layout.Right)},
		{label: "Move up", hint: "↑", action: paneMenuMove, direction: layout.Up, enabled: m.canMoveActivePane(layout.Up)},
		{label: "Move down", hint: "↓", action: paneMenuMove, direction: layout.Down, enabled: m.canMoveActivePane(layout.Down)},
		{separator: true},
		{label: "Balance panes", hint: "=", action: paneMenuBalance, enabled: paneCount > 1},
		{label: "Close pane", hint: "×", action: paneMenuClose, enabled: canClose},
	}
}

func availabilityHint(available bool) string {
	if available {
		return "ready"
	}
	return "missing"
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
	switch item.action {
	case paneMenuOpenSplitRight:
		m.openPaneContextSubmenu(paneMenuSplitRight)
	case paneMenuOpenSplitBelow:
		m.openPaneContextSubmenu(paneMenuSplitBelow)
	case paneMenuOpenNewTab:
		m.openPaneContextSubmenu(paneMenuNewTab)
	case paneMenuLaunchProfile:
		kind := m.contextMenu.kind
		m.closePaneContextMenu(false)
		switch kind {
		case paneMenuSplitBelow:
			m.splitActiveProfile(item.profile, layout.Rows)
		case paneMenuNewTab:
			m.newTabProfile(item.profile)
		default:
			m.splitActiveProfile(item.profile, layout.Columns)
		}
	case paneMenuMove:
		m.closePaneContextMenu(false)
		m.swapActivePane(item.direction)
	case paneMenuBalance:
		m.closePaneContextMenu(false)
		m.balanceActiveLayout()
	case paneMenuClose:
		m.closePaneContextMenu(false)
		m.closeActivePane()
	case paneMenuBack:
		m.openPaneContextSubmenu(paneMenuRoot)
	}
}

func (m *Model) openPaneContextSubmenu(kind paneContextMenuKind) {
	m.contextMenu.kind = kind
	m.contextMenu.selected = firstContextMenuSelection(m.paneContextMenuItems())
}

func (m *Model) handlePaneContextMenuKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "esc", "escape", "q":
		m.closePaneContextMenu(true)
	case "up", "k":
		m.movePaneContextMenuSelection(-1)
	case "down", "j":
		m.movePaneContextMenuSelection(1)
	case "enter", "right", "l":
		m.activatePaneContextMenuItem(m.contextMenu.selected)
	case "left", "h":
		if m.contextMenu.kind == paneMenuRoot {
			m.closePaneContextMenu(true)
		} else {
			m.openPaneContextSubmenu(paneMenuRoot)
		}
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

func (m *Model) paneContextMenuTitle() string {
	switch m.contextMenu.kind {
	case paneMenuSplitRight:
		return "Split right"
	case paneMenuSplitBelow:
		return "Split below"
	case paneMenuNewTab:
		return "New project"
	default:
		return "Pane"
	}
}

func (m *Model) renderPaneContextMenuOverlay(base string) string {
	geometry := m.paneContextMenuGeometry()
	items := m.paneContextMenuItems()
	innerWidth := max(0, geometry.width-2)
	border := lipgloss.NewStyle().Foreground(palette.accent)
	title := " " + m.paneContextMenuTitle() + " "
	title = ansi.Truncate(title, innerWidth, "")
	top := border.Render("┌" + title + strings.Repeat("─", max(0, innerWidth-ansi.StringWidth(title))) + "┐")

	lines := make([]string, 0, geometry.height)
	lines = append(lines, top)
	for index, item := range items {
		if item.separator {
			lines = append(lines, border.Render("├"+strings.Repeat("─", innerWidth)+"┤"))
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
		lines = append(lines, border.Render("│")+row+border.Render("│"))
	}
	lines = append(lines, border.Render("└"+strings.Repeat("─", innerWidth)+"┘"))
	menu := fitBlock(strings.Join(lines, "\n"), geometry.width, geometry.height)

	composed := lipgloss.NewCompositor(
		lipgloss.NewLayer(fitBlock(base, m.width, m.height)).X(0).Y(0).Z(0),
		lipgloss.NewLayer(menu).X(geometry.x).Y(geometry.y).Z(2),
	).Render()
	return fitBlock(composed, m.width, m.height)
}
