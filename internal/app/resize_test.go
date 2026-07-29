package app

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"split/internal/layout"
)

func TestAltRightDragResizesNearestPaneDividers(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()
	_, _ = model.Update(tea.WindowSizeMsg{Width: 150, Height: 42})
	model.splitActive(layout.Columns)
	model.splitActive(layout.Rows)

	active := model.active()
	paneID := active.activePane
	rect := active.root.Rects(model.workspaceRect())[paneID]
	beforeColumns := active.root.Ratio
	beforeRows := active.root.Second.Ratio
	model.mode = modeTerminal

	start := tea.Mouse{
		X:      rect.X + rect.Width*3/4,
		Y:      rect.Y + rect.Height*3/4,
		Button: tea.MouseRight,
		Mod:    tea.ModAlt,
	}
	_, _ = model.Update(tea.MouseClickMsg(start))
	if !model.resizeGesture.active || model.resizeGesture.sidebar {
		t.Fatalf("pane resize did not start: %#v", model.resizeGesture)
	}
	if model.resizeGesture.horizontal.edge != layout.ResizeLeft ||
		model.resizeGesture.vertical.edge != layout.ResizeTop {
		t.Fatalf("outer edges did not fall back to internal dividers: %#v", model.resizeGesture)
	}
	if model.contextMenu.open {
		t.Fatal("Alt+right-drag must not open the pane context menu")
	}
	if model.mode != modeTerminal || active.activePane != paneID {
		t.Fatal("resizing should preserve terminal-input mode on the selected pane")
	}

	drag := start
	drag.X += 8
	drag.Y += 3
	_, _ = model.Update(tea.MouseMotionMsg(drag))
	if active.root.Ratio <= beforeColumns {
		t.Fatalf("column ratio did not follow horizontal drag: %f -> %f", beforeColumns, active.root.Ratio)
	}
	if active.root.Second.Ratio <= beforeRows {
		t.Fatalf("row ratio did not follow vertical drag: %f -> %f", beforeRows, active.root.Second.Ratio)
	}
	if model.View().Cursor != nil {
		t.Fatal("terminal cursor should be hidden during a pane resize")
	}

	_, _ = model.Update(tea.MouseReleaseMsg(drag))
	if model.resizeGesture.active {
		t.Fatal("mouse release did not finish the pane resize")
	}
}

func TestAltRightDragSidebarWidthPersists(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.db")
	model, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = model.Update(tea.WindowSizeMsg{Width: 140, Height: 36})

	start := tea.Mouse{X: 8, Y: 10, Button: tea.MouseRight, Mod: tea.ModAlt}
	_, _ = model.Update(tea.MouseClickMsg(start))
	if !model.resizeGesture.active || !model.resizeGesture.sidebar {
		t.Fatalf("sidebar resize did not start: %#v", model.resizeGesture)
	}
	drag := start
	drag.X += 12
	_, _ = model.Update(tea.MouseMotionMsg(drag))
	if got := model.effectiveSidebarWidth(); got != sidebarWidth+12 {
		t.Fatalf("dragged sidebar width = %d, want %d", got, sidebarWidth+12)
	}
	_, _ = model.Update(tea.MouseReleaseMsg(drag))
	model.Close()

	restored, err := Open(root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	_, _ = restored.Update(tea.WindowSizeMsg{Width: 140, Height: 36})
	if got := restored.effectiveSidebarWidth(); got != sidebarWidth+12 {
		t.Fatalf("restored sidebar width = %d, want %d", got, sidebarWidth+12)
	}
}

func TestNarrowSidebarUsesCompactBrand(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()
	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model.sidebarSize = minimumSidebarWidth

	plain := ansi.Strip(model.renderSidebar(model.effectiveSidebarWidth(), 30))
	if !strings.Contains(plain, "split") {
		t.Fatalf("narrow sidebar is missing compact branding: %q", plain)
	}
	if strings.Contains(plain, sidebarBrandFrame[1]) {
		t.Fatal("narrow sidebar should not crop the full graphical wordmark")
	}
}

func TestResizeGestureFinishesWhenRightButtonStateIsLost(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()
	_, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	model.splitActive(layout.Columns)

	rect := model.active().root.Rects(model.workspaceRect())[model.active().activePane]
	start := tea.Mouse{
		X: rect.X + rect.Width/2, Y: rect.Y + rect.Height/2,
		Button: tea.MouseRight, Mod: tea.ModAlt,
	}
	_, _ = model.Update(tea.MouseClickMsg(start))
	if !model.resizeGesture.active {
		t.Fatal("resize gesture did not start")
	}
	lostRelease := start
	lostRelease.Button = tea.MouseNone
	_, _ = model.Update(tea.MouseMotionMsg(lostRelease))
	if model.resizeGesture.active {
		t.Fatal("buttonless motion should finish a stale resize gesture")
	}
}
