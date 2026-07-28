package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"split/internal/layout"
)

func TestInitialViewFillsWindow(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	const width = 100
	const height = 30
	_, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	view := model.View()

	lines := strings.Split(view.Content, "\n")
	if len(lines) != height {
		t.Fatalf("expected %d rendered rows, got %d", height, len(lines))
	}
	for row, line := range lines {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("row %d: expected width %d, got %d", row, width, got)
		}
	}
	if !strings.Contains(view.Content, "SPLIT") {
		t.Fatal("view does not contain the Split sidebar")
	}
	if !strings.Contains(view.Content, "PROJECTS") || !strings.Contains(view.Content, "+ New project") {
		t.Fatal("sidebar should expose the project switcher and new-project control")
	}
	if strings.Contains(view.Content, "LAUNCHERS") {
		t.Fatal("variable launch profiles should not occupy permanent sidebar space")
	}
	if strings.Contains(view.Content, "Command center") {
		t.Fatal("projects should start without the command-center pane")
	}
	plainLines := strings.Split(ansi.Strip(view.Content), "\n")
	if !strings.Contains(plainLines[0], "PowerShell") {
		t.Fatal("PowerShell pane should begin on the first row without a top tab strip")
	}
	if model.workspaceRect().Y != 0 {
		t.Fatal("workspace should reclaim the row formerly occupied by top tabs")
	}
	if !view.AltScreen {
		t.Fatal("view should use the alternate screen")
	}
}

func TestSidebarCreatesAndSwitchesProjectsWithMouse(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	firstProject := model.tabs[0]
	newProjectRow := sidebarProjectStart + len(model.tabs)
	model.handleMouseClick(tea.Mouse{X: 2, Y: newProjectRow, Button: tea.MouseLeft})

	if len(model.tabs) != 2 {
		t.Fatalf("clicking New project should create a second project, got %d", len(model.tabs))
	}
	if model.activeTab != 1 || model.tabs[1].title != "project 2" {
		t.Fatalf("new project should be selected and named consistently: active=%d title=%q", model.activeTab, model.tabs[1].title)
	}
	if model.tabs[1].activePane == firstProject.activePane {
		t.Fatal("new project should own an independent terminal workspace")
	}
	if got := len(model.tabs[1].root.Leaves()); got != 1 {
		t.Fatalf("new project should start as one full-size PowerShell pane, got %d panes", got)
	}
	if item := model.activePane(); item == nil || item.kind != paneTerminal || item.profile != profileShell {
		t.Fatal("new project should focus its PowerShell pane")
	}

	model.handleMouseClick(tea.Mouse{X: sidebarWidth + 2, Y: 0, Button: tea.MouseLeft})
	if model.activeTab != 1 {
		t.Fatal("clicking the top of the workspace must not act like a removed project tab")
	}

	model.handleMouseClick(tea.Mouse{X: 2, Y: sidebarProjectStart, Button: tea.MouseLeft})
	if model.activeTab != 0 || model.active() != firstProject {
		t.Fatal("clicking a project row should switch back to that workspace")
	}
}

func TestTerminalModeUsesEmulatedCursor(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model.mode = modeTerminal
	view := model.View()

	if view.Cursor == nil {
		t.Fatal("terminal mode should expose the emulated cursor")
	}
	if !view.Cursor.Blink {
		t.Fatal("the default terminal cursor should blink")
	}
}

func TestLauncherRendersAndOpensSelectedProfile(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	const width = 100
	const height = 30
	_, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	model.openLauncher()
	view := model.View()

	if !strings.Contains(view.Content, "Launch a pane") {
		t.Fatal("launcher modal is not visible")
	}
	if !strings.Contains(view.Content, "Codex") || !strings.Contains(view.Content, "Claude Code") {
		t.Fatal("launcher should list the supported coding agents")
	}
	if view.Cursor != nil {
		t.Fatal("launcher should hide the terminal cursor")
	}
	lines := strings.Split(view.Content, "\n")
	if len(lines) != height {
		t.Fatalf("launcher: expected %d rendered rows, got %d", height, len(lines))
	}
	for row, line := range lines {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("launcher row %d: expected width %d, got %d", row, width, got)
		}
	}

	before := len(model.active().root.Leaves())
	model.launcherSelected = 0
	model.launchSelected(layout.Columns, false)
	if model.launcherOpen {
		t.Fatal("launcher should close after a successful launch")
	}
	if got := len(model.active().root.Leaves()); got != before+1 {
		t.Fatalf("expected a new pane, got %d panes", got)
	}
	if active := model.activePane(); active == nil || active.profile != profileShell {
		t.Fatal("selected shell profile was not opened")
	}
}

func TestSplitsBalanceAndActivePaneCanMove(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	model.splitActive(layout.Columns)
	model.splitActive(layout.Columns)

	active := model.active()
	if got := len(active.root.Leaves()); got != 3 {
		t.Fatalf("expected three panes, got %d", got)
	}
	rects := active.root.Rects(model.workspaceRect())
	minimumWidth, maximumWidth := 1<<30, 0
	for _, rect := range rects {
		minimumWidth = min(minimumWidth, rect.Width)
		maximumWidth = max(maximumWidth, rect.Width)
	}
	if maximumWidth-minimumWidth > 1 {
		t.Fatalf("expected balanced pane widths, got min=%d max=%d", minimumWidth, maximumWidth)
	}

	activePaneID := active.activePane
	before := rects[activePaneID]
	model.swapActivePane(layout.Left)
	after := active.root.Rects(model.workspaceRect())[activePaneID]
	if after.X >= before.X {
		t.Fatalf("expected active pane to move left: before=%#v after=%#v", before, after)
	}
}

func TestThinAgentPaneUsesSafePlaceholder(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	model.splitActive(layout.Columns)
	model.activePane().profile = profileCodex
	model.mode = modeTerminal
	view := model.View()

	if !strings.Contains(view.Content, "Agent pane too narrow") {
		t.Fatal("thin agent pane should render its safe placeholder")
	}
	if view.Cursor != nil {
		t.Fatal("thin agent pane should hide its emulated cursor")
	}
}

func TestPaneFramePreservesExactContentRows(t *testing.T) {
	const width = 14
	const height = 8
	innerWidth := width - 2
	content := strings.Join([]string{
		"title",
		strings.Repeat(" ", innerWidth),
		"target",
		"",
		"",
		"",
	}, "\n")

	frame := ansi.Strip(renderPaneFrame("pane", content, width, height, palette.border))
	lines := strings.Split(frame, "\n")
	if len(lines) != height {
		t.Fatalf("expected %d frame rows, got %d", height, len(lines))
	}
	if !strings.Contains(lines[0], "pane") {
		t.Fatalf("pane title should be embedded in the top border: %#v", lines)
	}
	if !strings.Contains(lines[3], "target") {
		t.Fatalf("full-width blank row shifted later content: %#v", lines)
	}
	for row, line := range lines {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("row %d: expected frame width %d, got %d", row, width, got)
		}
	}
}

func TestColumnGapStaysCleanForWorkspaceHeight(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model.splitActive(layout.Columns)
	viewLines := strings.Split(ansi.Strip(model.View().Content), "\n")
	active := model.active()
	rects := active.root.Rects(model.workspaceRect())
	firstPane := rects[active.root.First.PaneID]
	dividerX := firstPane.X + firstPane.Width
	workspace := model.workspaceRect()

	for y := workspace.Y; y < workspace.Y+workspace.Height; y++ {
		line := []rune(viewLines[y])
		if dividerX >= len(line) || line[dividerX] != ' ' {
			t.Fatalf("row %d: expected clean split gap at column %d", y, dividerX)
		}
	}
}

func TestClickingTerminalPaneEntersInputMode(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	active := model.active()
	rects := active.root.Rects(model.workspaceRect())
	terminalRect := rects[active.activePane]
	model.handleMouseClick(tea.Mouse{
		X:      terminalRect.X + terminalRect.Width/2,
		Y:      terminalRect.Y + terminalRect.Height/2,
		Button: tea.MouseLeft,
	})
	if model.mode != modeTerminal || model.focus != focusPanes {
		t.Fatal("clicking a live terminal should focus it in terminal-input mode")
	}
}

func TestPaneContextMenuEnablesHoverTracking(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := model.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("normal view should use cell-motion mouse tracking, got %v", got)
	}

	active := model.active()
	rect := active.root.Rects(model.workspaceRect())[active.activePane]
	model.handleMouseClick(tea.Mouse{
		X:      rect.X + 2,
		Y:      rect.Y + 2,
		Button: tea.MouseRight,
	})
	if got := model.View().MouseMode; got != tea.MouseModeAllMotion {
		t.Fatalf("open context menu should enable no-button hover tracking, got %v", got)
	}

	model.closePaneContextMenu(true)
	if got := model.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("closing context menu should restore cell-motion tracking, got %v", got)
	}
}

func TestPaneContextMenuSupportsMouseAgentLaunch(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	const width = 100
	const height = 30
	_, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	active := model.active()
	terminalRect := active.root.Rects(model.workspaceRect())[active.activePane]

	model.handleMouseClick(tea.Mouse{
		X:      terminalRect.X + terminalRect.Width/2,
		Y:      terminalRect.Y + terminalRect.Height/2,
		Button: tea.MouseLeft,
	})
	if model.mode != modeTerminal {
		t.Fatal("terminal should be in input mode before opening its context menu")
	}

	model.handleMouseClick(tea.Mouse{
		X:      terminalRect.X + terminalRect.Width - 2,
		Y:      terminalRect.Y + terminalRect.Height - 2,
		Button: tea.MouseRight,
	})
	if !model.contextMenu.open || model.mode != modeNavigate {
		t.Fatal("right-click should open the pane context menu outside terminal-input mode")
	}
	geometry := model.paneContextMenuGeometry()
	if geometry.x < 0 || geometry.y < 0 || geometry.x+geometry.width > width || geometry.y+geometry.height > height {
		t.Fatalf("context menu should be clamped to the viewport: %#v", geometry)
	}
	view := model.View()
	if view.Cursor != nil {
		t.Fatal("context menu should hide the terminal cursor")
	}
	if !strings.Contains(view.Content, "Split right") || !strings.Contains(view.Content, "Close pane") {
		t.Fatal("root context menu is missing pane actions")
	}
	for row, line := range strings.Split(view.Content, "\n") {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("menu row %d: expected width %d, got %d", row, width, got)
		}
	}

	model.handlePaneContextMenuMotion(tea.Mouse{X: geometry.x + 2, Y: geometry.y + 2})
	if model.contextMenu.selected != 1 {
		t.Fatal("hovering a menu row should select it")
	}

	model.handlePaneContextMenuClick(tea.Mouse{
		X:      geometry.x + 2,
		Y:      geometry.y + 1,
		Button: tea.MouseLeft,
	})
	if model.contextMenu.kind != paneMenuSplitRight {
		t.Fatal("clicking Split right should open the agent profile submenu")
	}
	if !strings.Contains(model.View().Content, "Claude Code") {
		t.Fatal("agent submenu should include Claude Code")
	}

	before := len(active.root.Leaves())
	geometry = model.paneContextMenuGeometry()
	model.handlePaneContextMenuClick(tea.Mouse{
		X:      geometry.x + 2,
		Y:      geometry.y + 1,
		Button: tea.MouseLeft,
	})
	if model.contextMenu.open {
		t.Fatal("context menu should close after launching a profile")
	}
	if got := len(active.root.Leaves()); got != before+1 {
		t.Fatalf("expected mouse launch to create a pane, got %d panes", got)
	}
	if item := model.activePane(); item == nil || item.profile != profileShell {
		t.Fatal("mouse launch should create the selected PowerShell profile")
	}
}

func TestPaneContextMenuEscapeRestoresTerminalMode(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	active := model.active()
	rect := active.root.Rects(model.workspaceRect())[active.activePane]
	model.handleMouseClick(tea.Mouse{X: rect.X + 2, Y: rect.Y + 2, Button: tea.MouseLeft})
	model.handleMouseClick(tea.Mouse{X: rect.X + 2, Y: rect.Y + 2, Button: tea.MouseRight})
	_, _ = model.handlePaneContextMenuKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))

	if model.contextMenu.open {
		t.Fatal("escape should close the pane context menu")
	}
	if model.mode != modeTerminal {
		t.Fatal("canceling the context menu should restore terminal-input mode")
	}
}
