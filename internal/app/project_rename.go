package app

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"split/internal/terminal"
)

const (
	maxProjectNameRunes       = 80
	projectRenameDialogHeight = 8
	projectRenameCancelLabel  = " Cancel "
	projectRenameSaveLabel    = " Save "
)

type projectRenameButton uint8

const (
	projectRenameNoButton projectRenameButton = iota
	projectRenameCancel
	projectRenameSave
)

type projectRenameState struct {
	open                  bool
	projectIndex          int
	value                 []rune
	cursor                int
	selectAll             bool
	errorMessage          string
	hovered               projectRenameButton
	previousMode          mode
	previousFocus         focusArea
	previousSidebarCursor int
}

type projectRenameGeometry struct {
	x           int
	y           int
	width       int
	height      int
	inputX      int
	inputY      int
	inputWidth  int
	buttonY     int
	cancelX     int
	cancelWidth int
	saveX       int
	saveWidth   int
}

func (m *Model) openProjectRenameDialog(projectIndex int) {
	if projectIndex < 0 || projectIndex >= len(m.tabs) {
		return
	}
	if m.contextMenu.open {
		m.closePaneContextMenu(false)
	}
	if m.projectMenu.open {
		m.closeProjectContextMenu()
	}
	name := []rune(m.tabs[projectIndex].title)
	m.renameDialog = projectRenameState{
		open:                  true,
		projectIndex:          projectIndex,
		value:                 name,
		cursor:                len(name),
		selectAll:             true,
		previousMode:          m.mode,
		previousFocus:         m.focus,
		previousSidebarCursor: m.sidebarCursor,
	}
	m.mode = modeNavigate
	m.focus = focusSidebar
	m.sidebarCursor = projectIndex
	m.notice = ""
}

func (m *Model) closeProjectRenameDialog(restoreMode bool) {
	previousMode := m.renameDialog.previousMode
	previousFocus := m.renameDialog.previousFocus
	previousSidebarCursor := m.renameDialog.previousSidebarCursor
	m.renameDialog = projectRenameState{}
	m.focus = previousFocus
	m.sidebarCursor = max(0, min(previousSidebarCursor, len(m.tabs)))
	if !restoreMode || previousMode != modeTerminal {
		m.mode = modeNavigate
		return
	}
	item := m.activePane()
	if item == nil || item.session == nil {
		m.mode = modeNavigate
		return
	}
	current, _ := item.session.State()
	if current == terminal.Running {
		m.mode = modeTerminal
		m.focus = focusPanes
	} else {
		m.mode = modeNavigate
	}
}

func (m *Model) confirmProjectRename() {
	if !m.renameDialog.open ||
		m.renameDialog.projectIndex < 0 ||
		m.renameDialog.projectIndex >= len(m.tabs) {
		m.closeProjectRenameDialog(true)
		return
	}
	name := strings.TrimSpace(string(m.renameDialog.value))
	if name == "" {
		m.renameDialog.errorMessage = "Project name cannot be empty"
		m.renameDialog.selectAll = true
		m.renameDialog.cursor = len(m.renameDialog.value)
		return
	}
	projectIndex := m.renameDialog.projectIndex
	m.tabs[projectIndex].title = name
	m.closeProjectRenameDialog(true)
	m.notice = "Renamed project to " + name
	m.persist()
}

func (m *Model) insertProjectRenameText(value string) {
	if !m.renameDialog.open {
		return
	}
	incoming := sanitizedProjectNameRunes(value)
	if len(incoming) == 0 {
		return
	}
	if m.renameDialog.selectAll {
		m.renameDialog.value = nil
		m.renameDialog.cursor = 0
		m.renameDialog.selectAll = false
	}
	available := maxProjectNameRunes - len(m.renameDialog.value)
	if available <= 0 {
		return
	}
	if len(incoming) > available {
		incoming = incoming[:available]
	}
	cursor := max(0, min(m.renameDialog.cursor, len(m.renameDialog.value)))
	next := make([]rune, 0, len(m.renameDialog.value)+len(incoming))
	next = append(next, m.renameDialog.value[:cursor]...)
	next = append(next, incoming...)
	next = append(next, m.renameDialog.value[cursor:]...)
	m.renameDialog.value = next
	m.renameDialog.cursor = cursor + len(incoming)
	m.renameDialog.errorMessage = ""
}

func sanitizedProjectNameRunes(value string) []rune {
	result := make([]rune, 0, len(value))
	for _, character := range value {
		switch character {
		case '\r', '\n', '\t':
			character = ' '
		default:
			if unicode.IsControl(character) {
				continue
			}
		}
		result = append(result, character)
	}
	return result
}

func (m *Model) handleProjectRenameKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "esc", "escape", "ctrl+c":
		m.closeProjectRenameDialog(true)
	case "enter":
		m.confirmProjectRename()
	case "ctrl+a":
		m.renameDialog.selectAll = true
		m.renameDialog.cursor = len(m.renameDialog.value)
		m.renameDialog.errorMessage = ""
	case "ctrl+u":
		m.renameDialog.value = nil
		m.renameDialog.cursor = 0
		m.renameDialog.selectAll = false
		m.renameDialog.errorMessage = ""
	case "backspace":
		if m.renameDialog.selectAll {
			m.renameDialog.value = nil
			m.renameDialog.cursor = 0
			m.renameDialog.selectAll = false
		} else if m.renameDialog.cursor > 0 {
			cursor := min(m.renameDialog.cursor, len(m.renameDialog.value))
			m.renameDialog.value = append(
				m.renameDialog.value[:cursor-1],
				m.renameDialog.value[cursor:]...,
			)
			m.renameDialog.cursor = cursor - 1
		}
		m.renameDialog.errorMessage = ""
	case "delete":
		if m.renameDialog.selectAll {
			m.renameDialog.value = nil
			m.renameDialog.cursor = 0
			m.renameDialog.selectAll = false
		} else if m.renameDialog.cursor < len(m.renameDialog.value) {
			cursor := max(0, m.renameDialog.cursor)
			m.renameDialog.value = append(
				m.renameDialog.value[:cursor],
				m.renameDialog.value[cursor+1:]...,
			)
		}
		m.renameDialog.errorMessage = ""
	case "left":
		if m.renameDialog.selectAll {
			m.renameDialog.cursor = 0
			m.renameDialog.selectAll = false
		} else {
			m.renameDialog.cursor = max(0, m.renameDialog.cursor-1)
		}
	case "right":
		if m.renameDialog.selectAll {
			m.renameDialog.cursor = len(m.renameDialog.value)
			m.renameDialog.selectAll = false
		} else {
			m.renameDialog.cursor = min(len(m.renameDialog.value), m.renameDialog.cursor+1)
		}
	case "home":
		m.renameDialog.cursor = 0
		m.renameDialog.selectAll = false
	case "end":
		m.renameDialog.cursor = len(m.renameDialog.value)
		m.renameDialog.selectAll = false
	default:
		key := tea.Key(message)
		text := key.Text
		if text == "" && key.Mod == 0 && key.Code >= ' ' && key.Code != '\x7f' {
			text = string(key.Code)
		}
		m.insertProjectRenameText(text)
	}
	return m, nil
}

func (m *Model) handleProjectRenameMotion(mouse tea.Mouse) {
	m.renameDialog.hovered = m.projectRenameButtonAt(mouse.X, mouse.Y)
}

func (m *Model) handleProjectRenameClick(mouse tea.Mouse) {
	if mouse.Button == tea.MouseRight {
		m.closeProjectRenameDialog(true)
		return
	}
	if mouse.Button != tea.MouseLeft {
		return
	}
	switch m.projectRenameButtonAt(mouse.X, mouse.Y) {
	case projectRenameCancel:
		m.closeProjectRenameDialog(true)
		return
	case projectRenameSave:
		m.confirmProjectRename()
		return
	}

	geometry := m.projectRenameDialogGeometry()
	if mouse.Y == geometry.inputY &&
		mouse.X >= geometry.inputX &&
		mouse.X < geometry.inputX+geometry.inputWidth {
		_, start, _ := projectRenameVisibleValue(
			m.renameDialog.value,
			m.renameDialog.cursor,
			geometry.inputWidth,
		)
		column := max(0, mouse.X-geometry.inputX)
		m.renameDialog.cursor = projectRenameCursorAtColumn(
			m.renameDialog.value,
			start,
			column,
		)
		m.renameDialog.selectAll = false
		m.renameDialog.errorMessage = ""
		return
	}

	if mouse.X < geometry.x || mouse.X >= geometry.x+geometry.width ||
		mouse.Y < geometry.y || mouse.Y >= geometry.y+geometry.height {
		m.closeProjectRenameDialog(true)
	}
}

func (m *Model) projectRenameButtonAt(x, y int) projectRenameButton {
	geometry := m.projectRenameDialogGeometry()
	if y != geometry.buttonY {
		return projectRenameNoButton
	}
	switch {
	case x >= geometry.cancelX && x < geometry.cancelX+geometry.cancelWidth:
		return projectRenameCancel
	case x >= geometry.saveX && x < geometry.saveX+geometry.saveWidth:
		return projectRenameSave
	default:
		return projectRenameNoButton
	}
}

func (m *Model) projectRenameDialogGeometry() projectRenameGeometry {
	width := min(48, max(1, m.width-4))
	height := min(projectRenameDialogHeight, max(1, m.height))
	x := max(0, (m.width-width)/2)
	y := max(0, (m.height-height)/2)
	innerWidth := max(0, width-2)
	inputWidth := max(1, innerWidth-4)

	cancelWidth := ansi.StringWidth(projectRenameCancelLabel)
	saveWidth := ansi.StringWidth(projectRenameSaveLabel)
	buttonGap := 2
	buttonsWidth := cancelWidth + buttonGap + saveWidth
	buttonLeft := max(0, innerWidth-2-buttonsWidth)
	cancelX := x + 1 + buttonLeft

	return projectRenameGeometry{
		x:           x,
		y:           y,
		width:       width,
		height:      height,
		inputX:      x + 3,
		inputY:      y + 3,
		inputWidth:  inputWidth,
		buttonY:     y + 6,
		cancelX:     cancelX,
		cancelWidth: cancelWidth,
		saveX:       cancelX + cancelWidth + buttonGap,
		saveWidth:   saveWidth,
	}
}

func projectRenameVisibleValue(value []rune, cursor, width int) (string, int, int) {
	if width <= 0 {
		return "", 0, 0
	}
	cursor = max(0, min(cursor, len(value)))
	start := 0
	for start < cursor && ansi.StringWidth(string(value[start:cursor])) >= width {
		start++
	}
	end := start
	for end < len(value) {
		if ansi.StringWidth(string(value[start:end+1])) > width {
			break
		}
		end++
	}
	return string(value[start:end]), start, ansi.StringWidth(string(value[start:cursor]))
}

func projectRenameCursorAtColumn(value []rune, start, column int) int {
	start = max(0, min(start, len(value)))
	column = max(0, column)
	used := 0
	for index := start; index < len(value); index++ {
		characterWidth := max(1, ansi.StringWidth(string(value[index])))
		if used+characterWidth > column {
			return index
		}
		used += characterWidth
		if used >= column {
			return index + 1
		}
	}
	return len(value)
}

func (m *Model) renderProjectRenameCursor() *tea.Cursor {
	if !m.renameDialog.open || m.renameDialog.selectAll ||
		m.width < 42 || m.height < 12 {
		return nil
	}
	geometry := m.projectRenameDialogGeometry()
	_, _, cursorColumn := projectRenameVisibleValue(
		m.renameDialog.value,
		m.renameDialog.cursor,
		geometry.inputWidth,
	)
	cursor := tea.NewCursor(
		geometry.inputX+min(cursorColumn, geometry.inputWidth-1),
		geometry.inputY,
	)
	cursor.Shape = tea.CursorBar
	cursor.Blink = true
	cursor.Color = palette.text
	return cursor
}

func (m *Model) renderProjectRenameOverlay(base string) string {
	geometry := m.projectRenameDialogGeometry()
	innerWidth := max(0, geometry.width-2)
	border := lipgloss.NewStyle().Foreground(palette.accent)
	body := lipgloss.NewStyle().Foreground(palette.text).Background(palette.surface)
	labelStyle := lipgloss.NewStyle().Foreground(palette.muted).Background(palette.surface)
	fieldStyle := lipgloss.NewStyle().Foreground(palette.text).Background(palette.surfaceAlt)
	selectedStyle := lipgloss.NewStyle().Foreground(palette.background).Background(palette.accent)

	title := ansi.Truncate(" Rename project ", innerWidth, "")
	lines := []string{
		border.Render("\u250c" + title + strings.Repeat("\u2500", max(0, innerWidth-ansi.StringWidth(title))) + "\u2510"),
		border.Render("\u2502") + body.Render(strings.Repeat(" ", innerWidth)) + border.Render("\u2502"),
		border.Render("\u2502") + labelStyle.Render(fitLine("  Project name", innerWidth)) + border.Render("\u2502"),
	}

	visible, _, _ := projectRenameVisibleValue(
		m.renameDialog.value,
		m.renameDialog.cursor,
		geometry.inputWidth,
	)
	fieldPadding := max(0, geometry.inputWidth-ansi.StringWidth(visible))
	field := ""
	if m.renameDialog.selectAll && visible != "" {
		field = selectedStyle.Render(visible) +
			fieldStyle.Render(strings.Repeat(" ", fieldPadding))
	} else {
		field = fieldStyle.Render(visible + strings.Repeat(" ", fieldPadding))
	}
	lines = append(lines,
		border.Render("\u2502")+
			body.Render("  ")+field+body.Render("  ")+
			border.Render("\u2502"),
	)

	hint := "  Enter save  \u00b7  Esc cancel"
	hintStyle := labelStyle
	if m.renameDialog.errorMessage != "" {
		hint = "  " + m.renameDialog.errorMessage
		hintStyle = lipgloss.NewStyle().Foreground(palette.red).Background(palette.surface).Bold(true)
	}
	lines = append(lines,
		border.Render("\u2502")+hintStyle.Render(fitLine(hint, innerWidth))+border.Render("\u2502"),
		border.Render("\u2502")+body.Render(strings.Repeat(" ", innerWidth))+border.Render("\u2502"),
	)

	cancelStyle := lipgloss.NewStyle().Foreground(palette.text).Background(palette.surfaceAlt)
	saveStyle := lipgloss.NewStyle().Foreground(palette.background).Background(palette.accent).Bold(true)
	if m.renameDialog.hovered == projectRenameCancel {
		cancelStyle = lipgloss.NewStyle().Foreground(palette.background).Background(palette.text).Bold(true)
	}
	if m.renameDialog.hovered == projectRenameSave {
		saveStyle = lipgloss.NewStyle().Foreground(palette.background).Background(palette.text).Bold(true)
	}
	buttonLeading := max(0, geometry.cancelX-(geometry.x+1))
	buttonTrailing := max(
		0,
		innerWidth-buttonLeading-geometry.cancelWidth-2-geometry.saveWidth,
	)
	lines = append(lines,
		border.Render("\u2502")+
			body.Render(strings.Repeat(" ", buttonLeading))+
			cancelStyle.Render(projectRenameCancelLabel)+
			body.Render("  ")+
			saveStyle.Render(projectRenameSaveLabel)+
			body.Render(strings.Repeat(" ", buttonTrailing))+
			border.Render("\u2502"),
		border.Render("\u2514"+strings.Repeat("\u2500", innerWidth)+"\u2518"),
	)

	dialog := fitBlock(strings.Join(lines, "\n"), geometry.width, geometry.height)
	composed := lipgloss.NewCompositor(
		lipgloss.NewLayer(fitBlock(base, m.width, m.height)).X(0).Y(0).Z(0),
		lipgloss.NewLayer(dialog).X(geometry.x).Y(geometry.y).Z(3),
	).Render()
	return fitBlock(composed, m.width, m.height)
}
