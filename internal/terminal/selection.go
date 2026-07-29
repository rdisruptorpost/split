package terminal

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

type selectionPoint struct {
	row int
	col int
}

type terminalSelection struct {
	anchor  selectionPoint
	cursor  selectionPoint
	started bool
	visible bool
}

func (selection terminalSelection) ordered() (selectionPoint, selectionPoint) {
	start, end := selection.anchor, selection.cursor
	if start.row > end.row || (start.row == end.row && start.col > end.col) {
		start, end = end, start
	}
	return start, end
}

func (selection terminalSelection) contains(row, col int) bool {
	if !selection.visible {
		return false
	}
	start, end := selection.ordered()
	if row < start.row || row > end.row {
		return false
	}
	if start.row == end.row {
		return col >= start.col && col <= end.col
	}
	if row == start.row {
		return col >= start.col
	}
	if row == end.row {
		return col <= end.col
	}
	return true
}

// BeginSelection records a potential selection at a pane-relative viewport cell.
// A plain click remains invisible until the pointer moves to another cell.
func (s *Session) BeginSelection(x, y int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	point, ok := s.viewportPointLocked(x, y)
	if !ok {
		s.selection = terminalSelection{}
		return false
	}
	s.selection = terminalSelection{anchor: point, cursor: point, started: true}
	return true
}

// UpdateSelection extends the active selection and reports whether it is visible.
func (s *Session) UpdateSelection(x, y int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.selection.started {
		return false
	}
	point, ok := s.viewportPointLocked(x, y)
	if !ok {
		return false
	}
	s.selection.cursor = point
	s.selection.visible = point != s.selection.anchor
	return s.selection.visible
}

// EndSelection finalizes a drag. Releasing without moving clears the anchor.
func (s *Session) EndSelection(x, y int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.selection.started {
		return false
	}
	if point, ok := s.viewportPointLocked(x, y); ok {
		s.selection.cursor = point
		s.selection.visible = point != s.selection.anchor
	}
	if !s.selection.visible {
		s.selection = terminalSelection{}
		return false
	}
	return true
}

func (s *Session) ClearSelection() {
	s.mu.Lock()
	s.selection = terminalSelection{}
	s.mu.Unlock()
}

func (s *Session) HasSelection() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selection.visible
}

// SelectedText returns plain text in reading order. Terminal padding at the
// right edge is trimmed while deliberate spaces inside the selection remain.
func (s *Session) SelectedText() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.selection.visible {
		return "", false
	}

	start, end := s.selection.ordered()
	maximumRow := s.emulator.ScrollbackLen() + s.emulator.Height() - 1
	if maximumRow < 0 || end.row < 0 || start.row > maximumRow {
		return "", false
	}
	start.row = max(0, start.row)
	end.row = min(maximumRow, end.row)
	width := s.emulator.Width()
	if width <= 0 {
		return "", false
	}

	lines := make([]string, 0, end.row-start.row+1)
	for row := start.row; row <= end.row; row++ {
		firstColumn := 0
		lastColumn := width - 1
		if row == start.row {
			firstColumn = max(0, min(start.col, width-1))
		}
		if row == end.row {
			lastColumn = max(0, min(end.col, width-1))
		}
		lines = append(lines, strings.TrimRight(s.selectedLineTextLocked(row, firstColumn, lastColumn), " "))
	}
	return strings.Join(lines, "\n"), true
}

func (s *Session) viewportPointLocked(x, y int) (selectionPoint, bool) {
	width := s.emulator.Width()
	height := s.emulator.Height()
	if width <= 0 || height <= 0 {
		return selectionPoint{}, false
	}
	x = max(0, min(x, width-1))
	y = max(0, min(y, height-1))
	scrollbackLength := s.emulator.ScrollbackLen()
	offset := max(0, min(s.scrollOffset, scrollbackLength))
	row := scrollbackLength - offset + y
	for x > 0 {
		cell := terminalCellAt(s.emulator, row, x)
		if cell == nil || cell.Width != 0 {
			break
		}
		x--
	}
	return selectionPoint{row: row, col: x}, true
}

func (s *Session) selectedLineTextLocked(row, firstColumn, lastColumn int) string {
	if firstColumn > lastColumn {
		firstColumn, lastColumn = lastColumn, firstColumn
	}
	var result strings.Builder
	for column := firstColumn; column <= lastColumn; column++ {
		cell := terminalCellAt(s.emulator, row, column)
		switch {
		case cell == nil:
			result.WriteByte(' ')
		case cell.Width == 0:
			// Continuation cell for a wide grapheme; the leading cell already
			// contributed its complete content.
		case cell.Content == "":
			result.WriteByte(' ')
		default:
			result.WriteString(cell.Content)
		}
	}
	return result.String()
}

func renderTerminalViewport(emulator *vt.Emulator, offset int, selection *terminalSelection) string {
	lines, top := terminalViewportLines(emulator, offset)
	if selection != nil && selection.visible {
		for viewportRow := range lines {
			absoluteRow := top + viewportRow
			for column := range lines[viewportRow] {
				if !selection.contains(absoluteRow, column) {
					continue
				}
				cell := &lines[viewportRow][column]
				if cell.IsZero() {
					// Preserve zero-width continuation cells for wide graphemes.
					continue
				}
				cell.Style.Fg = colorSelectionForeground
				cell.Style.Bg = colorSelectionBackground
				cell.Style.Attrs &^= uv.AttrReverse
			}
		}
	}
	return lines.Render()
}

func terminalViewportLines(emulator *vt.Emulator, offset int) (uv.Lines, int) {
	width := emulator.Width()
	height := emulator.Height()
	scrollbackLength := emulator.ScrollbackLen()
	offset = max(0, min(offset, scrollbackLength))
	top := scrollbackLength - offset
	lines := make(uv.Lines, height)

	for row := range height {
		line := uv.NewLine(width)
		absoluteRow := top + row
		if absoluteRow < scrollbackLength {
			if scrollback := emulator.Scrollback(); scrollback != nil {
				copy(line, scrollback.Line(absoluteRow))
			}
		} else {
			screenRow := absoluteRow - scrollbackLength
			if screenRow >= 0 && screenRow < height {
				for column := range width {
					if cell := emulator.CellAt(column, screenRow); cell != nil {
						line[column] = *cell
					}
				}
			}
		}
		lines[row] = line
	}
	return lines, top
}

func terminalCellAt(emulator *vt.Emulator, absoluteRow, column int) *uv.Cell {
	scrollbackLength := emulator.ScrollbackLen()
	if absoluteRow < 0 || column < 0 || column >= emulator.Width() {
		return nil
	}
	if absoluteRow < scrollbackLength {
		if scrollback := emulator.Scrollback(); scrollback != nil {
			return scrollback.CellAt(column, absoluteRow)
		}
		return nil
	}
	screenRow := absoluteRow - scrollbackLength
	if screenRow < 0 || screenRow >= emulator.Height() {
		return nil
	}
	return emulator.CellAt(column, screenRow)
}
