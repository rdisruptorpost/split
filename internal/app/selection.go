package app

import (
	tea "charm.land/bubbletea/v2"

	"split/internal/layout"
)

type terminalSelectionGesture struct {
	paneID   string
	dragging bool
}

func paneInnerRect(rect layout.Rect) (layout.Rect, bool) {
	inner := layout.Rect{
		X:      rect.X + 1,
		Y:      rect.Y + 1,
		Width:  rect.Width - 2,
		Height: rect.Height - 2,
	}
	return inner, inner.Width > 0 && inner.Height > 0
}

func (m *Model) activePaneRect(paneID string) (layout.Rect, bool) {
	active := m.active()
	if active == nil || active.root == nil {
		return layout.Rect{}, false
	}
	rect, ok := active.root.Rects(m.workspaceRect())[paneID]
	return rect, ok
}

func (m *Model) clearTerminalSelection() {
	paneID := m.selectionGesture.paneID
	if paneID != "" {
		if item := m.panes[paneID]; item != nil && item.session != nil {
			item.session.ClearSelection()
		}
	}
	m.selectionGesture = terminalSelectionGesture{}
}

func (m *Model) beginTerminalSelection(paneID string, rect layout.Rect, mouse tea.Mouse) bool {
	inner, ok := paneInnerRect(rect)
	if !ok || !inner.Contains(mouse.X, mouse.Y) {
		m.clearTerminalSelection()
		return false
	}
	item := m.panes[paneID]
	if item == nil || item.session == nil {
		m.clearTerminalSelection()
		return false
	}

	m.clearTerminalSelection()
	if !item.session.BeginSelection(mouse.X-inner.X, mouse.Y-inner.Y) {
		return false
	}
	m.selectionGesture = terminalSelectionGesture{paneID: paneID, dragging: true}
	return true
}

func (m *Model) handleTerminalSelectionMotion(mouse tea.Mouse) {
	gesture := m.selectionGesture
	if !gesture.dragging || mouse.Button != tea.MouseLeft {
		return
	}
	item := m.panes[gesture.paneID]
	rect, ok := m.activePaneRect(gesture.paneID)
	if item == nil || item.session == nil || !ok {
		m.clearTerminalSelection()
		return
	}
	inner, ok := paneInnerRect(rect)
	if !ok {
		m.clearTerminalSelection()
		return
	}
	x, y := clampMouseToRect(mouse, inner)
	item.session.UpdateSelection(x-inner.X, y-inner.Y)
}

func (m *Model) handleTerminalSelectionRelease(mouse tea.Mouse) {
	gesture := m.selectionGesture
	if !gesture.dragging || mouse.Button != tea.MouseLeft {
		return
	}
	item := m.panes[gesture.paneID]
	rect, ok := m.activePaneRect(gesture.paneID)
	if item == nil || item.session == nil || !ok {
		m.clearTerminalSelection()
		return
	}
	inner, ok := paneInnerRect(rect)
	if !ok {
		m.clearTerminalSelection()
		return
	}
	x, y := clampMouseToRect(mouse, inner)
	if !item.session.EndSelection(x-inner.X, y-inner.Y) {
		m.selectionGesture = terminalSelectionGesture{}
		return
	}
	m.selectionGesture.dragging = false
}

func clampMouseToRect(mouse tea.Mouse, rect layout.Rect) (int, int) {
	x := max(rect.X, min(mouse.X, rect.X+rect.Width-1))
	y := max(rect.Y, min(mouse.Y, rect.Y+rect.Height-1))
	return x, y
}

func (m *Model) copyPaneSelection(paneID string) bool {
	item := m.panes[paneID]
	if item == nil || item.session == nil {
		return false
	}
	text, ok := item.session.SelectedText()
	if !ok {
		return false
	}
	m.clipboardText = text
	m.clipboardPending = true
	m.notice = "Copied selection"
	item.session.ClearSelection()
	m.selectionGesture = terminalSelectionGesture{}
	return true
}

// TakeClipboardRequest returns a one-shot clipboard side effect for the
// detached runtime. Clipboard updates cannot be represented by a rendered
// frame alone, so the runtime carries this payload to the connected client.
func (m *Model) TakeClipboardRequest() (string, bool) {
	if !m.clipboardPending {
		return "", false
	}
	text := m.clipboardText
	m.clipboardText = ""
	m.clipboardPending = false
	return text, true
}
