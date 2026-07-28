package app

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"split/internal/layout"
	"split/internal/terminal"
)

func (m *Model) View() tea.View {
	content := m.render()
	view := tea.NewView(content)
	view.AltScreen = true
	view.ReportFocus = true
	view.MouseMode = tea.MouseModeCellMotion
	if m.contextMenu.open {
		view.MouseMode = tea.MouseModeAllMotion
	}
	view.BackgroundColor = palette.background
	view.ForegroundColor = palette.text
	view.WindowTitle = "Split — " + m.projectName()
	view.Cursor = m.renderCursor()
	return view
}

func (m *Model) renderCursor() *tea.Cursor {
	if m.contextMenu.open || m.mode != modeTerminal || m.focus != focusPanes {
		return nil
	}
	active := m.active()
	item := m.activePane()
	if active == nil || item == nil || item.session == nil {
		return nil
	}

	state, _ := item.session.State()
	cursorState := item.session.Cursor()
	if state != terminal.Running || !cursorState.Visible {
		return nil
	}

	rect, ok := active.root.Rects(m.workspaceRect())[item.id]
	bodyWidth := max(1, rect.Width-2)
	bodyHeight := max(1, rect.Height-2)
	if !ok || cursorState.X < 0 || cursorState.Y < 0 ||
		cursorState.X >= bodyWidth || cursorState.Y >= bodyHeight {
		return nil
	}

	cursor := tea.NewCursor(rect.X+1+cursorState.X, rect.Y+1+cursorState.Y)
	cursor.Color = cursorState.Color
	cursor.Blink = cursorState.Blink
	switch cursorState.Style {
	case terminal.CursorUnderline:
		cursor.Shape = tea.CursorUnderline
	case terminal.CursorBar:
		cursor.Shape = tea.CursorBar
	default:
		cursor.Shape = tea.CursorBlock
	}
	return cursor
}

func (m *Model) render() string {
	if m.width < 42 || m.height < 12 {
		message := styles.logo.Render("SPLIT") + "\n\n" +
			styles.text.Render("The terminal is too small.") + "\n" +
			styles.muted.Render("Resize to at least 42 × 12.")
		return placeBlock(message, m.width, m.height)
	}

	sidebarWidth := m.effectiveSidebarWidth()
	mainWidth := m.width - sidebarWidth
	main := lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderWorkspace(mainWidth, m.height-statusHeight),
		m.renderStatus(mainWidth),
	)
	main = fitBlock(main, mainWidth, m.height)

	result := main
	if sidebarWidth > 0 {
		result = lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.renderSidebar(sidebarWidth, m.height),
			main,
		)
	}

	if m.contextMenu.open {
		result = m.renderPaneContextMenuOverlay(result)
	}
	return result
}

func (m *Model) renderSidebar(width, height int) string {
	contentWidth := max(1, width-1)
	lines := make([]string, 0, height)
	for _, line := range renderSidebarBrand() {
		lines = append(lines, " "+line)
	}
	lines = append(lines, " "+styles.eyebrow.Render("PROJECTS"))

	for index, item := range m.tabs {
		active := index == m.activeTab
		cursor := index == m.sidebarCursor && m.focus == focusSidebar
		dot := m.renderTabStatus(item)
		label := dot + " " + item.title
		if cursor {
			label = "› " + label
		} else {
			label = "  " + label
		}

		suffix := fmt.Sprintf("%d", len(item.root.Leaves()))
		gap := max(1, contentWidth-ansi.StringWidth(label)-ansi.StringWidth(suffix)-1)
		row := label + strings.Repeat(" ", gap) + styles.muted.Render(suffix)
		if active {
			row = styles.activeSession.Width(contentWidth).Render(row)
		} else {
			row = styles.session.Width(contentWidth).Render(row)
		}
		lines = append(lines, row)
	}

	newProjectLabel := "  + New project"
	newProjectRow := lipgloss.NewStyle().
		Width(contentWidth).
		Foreground(palette.accent).
		Bold(true).
		Render(newProjectLabel)
	if m.focus == focusSidebar && m.sidebarCursor == len(m.tabs) {
		newProjectRow = styles.activeSession.Width(contentWidth).Render("› + New project")
	}
	lines = append(lines, newProjectRow)

	for len(lines) < height-2 {
		lines = append(lines, "")
	}
	lines = append(lines,
		" "+styles.muted.Render("ctrl+b")+" "+styles.text.Render("menu")+"  "+styles.muted.Render("q")+" "+styles.text.Render("detach"),
		" "+styles.muted.Render("tab")+"    "+styles.text.Render("switch focus"),
	)

	border := lipgloss.NewStyle().Foreground(palette.border).Render("│")
	output := make([]string, height)
	for row := range height {
		line := ""
		if row < len(lines) {
			line = lines[row]
		}
		output[row] = fitLine(line, contentWidth) + border
	}
	return strings.Join(output, "\n")
}

func (m *Model) renderTabStatus(item *tab) string {
	state := terminal.Exited
	for _, paneID := range item.root.Leaves() {
		current := m.panes[paneID]
		if current == nil || current.kind != paneTerminal {
			continue
		}
		if current.err != nil {
			return lipgloss.NewStyle().Foreground(palette.red).Render("●")
		}
		if current.session == nil {
			state = terminal.Starting
			continue
		}
		paneState, _ := current.session.State()
		switch paneState {
		case terminal.Failed:
			return lipgloss.NewStyle().Foreground(palette.red).Render("●")
		case terminal.Running:
			state = terminal.Running
		case terminal.Starting:
			if state != terminal.Running {
				state = terminal.Starting
			}
		}
	}
	switch state {
	case terminal.Running:
		return lipgloss.NewStyle().Foreground(palette.green).Render("●")
	case terminal.Starting:
		return lipgloss.NewStyle().Foreground(palette.yellow).Render("●")
	default:
		return styles.muted.Render("○")
	}
}

func (m *Model) renderWorkspace(width, height int) string {
	active := m.active()
	if active == nil {
		return fitBlock("", width, height)
	}
	return fitBlock(m.renderNode(active.root, width, height), width, height)
}

func (m *Model) renderNode(node *layout.Node, width, height int) string {
	if node == nil {
		return fitBlock("", width, height)
	}
	if node.IsLeaf() {
		return m.renderPane(m.panes[node.PaneID], width, height)
	}

	first, second := layout.SplitSizes(node.Axis, width, height, node.Ratio)
	if node.Axis == layout.Columns {
		return lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.renderNode(node.First, first, height),
			fitBlock("", layout.Gap, height),
			m.renderNode(node.Second, second, height),
		)
	}
	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderNode(node.First, width, first),
		strings.Repeat(" ", width),
		m.renderNode(node.Second, width, second),
	)
}

func (m *Model) renderPane(item *pane, width, height int) string {
	if item == nil {
		return fitBlock("", width, height)
	}
	width = max(1, width)
	height = max(1, height)
	innerWidth := max(1, width-2)

	active := m.active()
	focused := active != nil && active.activePane == item.id && m.focus == focusPanes
	borderColor := palette.border
	if focused {
		borderColor = palette.accent
	}

	title := styles.paneTitle.Render(item.title)
	if item.kind == paneTerminal {
		title += " " + m.renderTerminalState(item)
	}

	bodyHeight := max(0, height-2)
	var body string
	switch item.kind {
	case paneOverview:
		body = m.renderOverview(innerWidth, bodyHeight)
	case paneTerminal:
		body = m.renderTerminal(item, innerWidth, bodyHeight)
	}
	return renderPaneFrame(title, body, width, height, borderColor)
}

func renderPaneFrame(title, body string, width, height int, borderColor color.Color) string {
	if width < 2 || height < 2 {
		return fitBlock(body, width, height)
	}
	innerWidth := width - 2
	bodyHeight := height - 2
	border := lipgloss.NewStyle().Foreground(borderColor)

	header := ansi.Truncate(title, max(0, innerWidth-3), "")
	headerWidth := ansi.StringWidth(header)
	top := border.Render("┌" + strings.Repeat("─", innerWidth) + "┐")
	if headerWidth > 0 && innerWidth >= 4 {
		remaining := max(0, innerWidth-headerWidth-3)
		top = border.Render("┌─ ") + header + border.Render(" "+strings.Repeat("─", remaining)+"┐")
	}

	lines := strings.Split(fitBlock(body, innerWidth, bodyHeight), "\n")
	output := make([]string, 0, height)
	output = append(output, top)
	for _, line := range lines {
		output = append(output, border.Render("│")+fitLine(line, innerWidth)+border.Render("│"))
	}
	output = append(output, border.Render("└"+strings.Repeat("─", innerWidth)+"┘"))
	return fitBlock(strings.Join(output, "\n"), width, height)
}

func (m *Model) renderTerminal(item *pane, width, height int) string {
	if item.err != nil {
		content := lipgloss.NewStyle().Foreground(palette.red).Render("Terminal failed to start") +
			"\n\n" + styles.muted.Render(item.err.Error())
		return fitBlock(content, width, height)
	}
	if item.session == nil {
		return fitBlock(styles.muted.Render("Starting terminal…"), width, height)
	}
	return fitBlock(item.session.Render(), width, height)
}

func (m *Model) renderTerminalState(item *pane) string {
	if item.err != nil {
		return lipgloss.NewStyle().Foreground(palette.red).Render("failed")
	}
	if item.session == nil {
		return styles.muted.Render("starting")
	}
	state, _ := item.session.State()
	switch state {
	case terminal.Running:
		return lipgloss.NewStyle().Foreground(palette.green).Render("● live")
	case terminal.Exited:
		return styles.muted.Render("○ exited")
	case terminal.Failed:
		return lipgloss.NewStyle().Foreground(palette.red).Render("● failed")
	default:
		return lipgloss.NewStyle().Foreground(palette.yellow).Render("● starting")
	}
}

func (m *Model) renderOverview(width, height int) string {
	if width < 22 || height < 8 {
		return fitBlock(
			styles.text.Render("A terminal-native workspace.")+"\n\n"+
				styles.muted.Render("Click the shell or press enter."),
			width,
			height,
		)
	}

	accent := lipgloss.NewStyle().Foreground(palette.accent).Bold(true)
	key := lipgloss.NewStyle().Foreground(palette.secondary)
	content :=
		accent.Render("Your agents, one workspace.") + "\n" +
			styles.muted.Render("The first vertical slice is running.") + "\n\n" +
			styles.eyebrow.Render("QUICK START") + "\n" +
			key.Render("click/enter") + " focus a terminal\n" +
			key.Render("right-click") + " pane action menu\n" +
			key.Render("ctrl+b  v") + "   split right\n" +
			key.Render("ctrl+b  s") + "   split down\n" +
			key.Render("ctrl+b  c") + "   new project\n" +

			key.Render("ctrl+b  x") + "   close focused pane\n" +
			key.Render("ctrl+b  hjkl") + " move focused pane\n" +
			key.Render("ctrl+b  =") + "   balance pane sizes\n" +
			key.Render("ctrl+b  n") + "   navigate panes\n\n" +
			styles.eyebrow.Render("FOUNDATION") + "\n" +
			lipgloss.NewStyle().Foreground(palette.green).Render("✓") + " sidebar and tabs\n" +
			lipgloss.NewStyle().Foreground(palette.green).Render("✓") + " split-tree layout\n" +
			lipgloss.NewStyle().Foreground(palette.green).Render("✓") + " prefix/navigation modes\n" +
			lipgloss.NewStyle().Foreground(palette.green).Render("✓") + " live ConPTY terminal\n" +
			lipgloss.NewStyle().Foreground(palette.green).Render("✓") + " persistent terminal runtime"
	return fitBlock(content, width, height)
}

func (m *Model) renderStatus(width int) string {
	modeLabel := " NAV "
	modeColor := palette.accent
	if m.contextMenu.open {
		modeLabel = " MENU "
		modeColor = palette.secondary
	} else {
		switch m.mode {
		case modeTerminal:
			modeLabel = " TERM "
			modeColor = palette.green
		case modePrefix:
			modeLabel = " PREFIX "
			modeColor = palette.secondary
		}
	}
	badge := lipgloss.NewStyle().
		Foreground(palette.background).
		Background(modeColor).
		Bold(true).
		Render(modeLabel)

	hint := " "
	if m.contextMenu.open {
		hint += "click an action  hover to select  esc close"
	} else {
		switch m.mode {
		case modeTerminal:
			hint += "ctrl+b  command prefix"
		case modePrefix:
			hint += "v/s split  x close  hjkl move pane  = balance  n navigate  q detach"
		default:
			hint += "click terminal  right-click pane menu  arrows/hjkl focus  ctrl+b commands"
		}
	}
	left := badge + styles.status.Render(hint)
	if m.notice != "" {
		left = badge + styles.status.Render(" "+m.notice)
	}

	active := m.active()
	right := ""
	if active != nil {
		right = fmt.Sprintf(" %d pane", len(active.root.Leaves()))
		if len(active.root.Leaves()) != 1 {
			right += "s"
		}
		right += "  " + time.Now().Format("15:04") + " "
	}
	right = styles.status.Render(right)

	available := width - ansi.StringWidth(right)
	if available < 1 {
		return fitLine(right, width)
	}
	left = fitLine(left, available)
	return fitLine(left+right, width)
}

func fitLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = ansi.Truncate(value, width, "")
	padding := width - ansi.StringWidth(value)
	if padding > 0 {
		value += strings.Repeat(" ", padding)
	}
	return value
}

func fitBlock(value string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	output := make([]string, height)
	for row := range height {
		if row < len(lines) {
			output[row] = fitLine(lines[row], width)
		} else {
			output[row] = strings.Repeat(" ", width)
		}
	}
	return strings.Join(output, "\n")
}

func placeBlock(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	blockHeight := len(lines)
	top := max(0, (height-blockHeight)/2)
	output := make([]string, height)
	for row := range height {
		if row >= top && row < top+blockHeight {
			line := lines[row-top]
			lineWidth := ansi.StringWidth(line)
			left := max(0, (width-lineWidth)/2)
			output[row] = fitLine(strings.Repeat(" ", left)+line, width)
		} else {
			output[row] = strings.Repeat(" ", width)
		}
	}
	return strings.Join(output, "\n")
}

func shortenPath(path string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(path) <= width {
		return path
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	return "…" + ansi.TruncateLeft(path, ansi.StringWidth(path)-width+1, "")
}
