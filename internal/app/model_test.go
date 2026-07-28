package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"split/internal/agent"
	"split/internal/layout"
	"split/internal/terminal"
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
	plainView := ansi.Strip(view.Content)
	if !strings.Contains(plainView, sidebarBrandFrame[1]) {
		t.Fatal("view does not contain the Split graphic")
	}
	if strings.Contains(plainView, "SPLIT ·") {
		t.Fatal("legacy text branding should be replaced by the graphic")
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
	plainLines := strings.Split(plainView, "\n")
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
	if item := model.activePane(); item == nil || item.kind != paneTerminal || item.title != "PowerShell" {
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

func TestModelForwardsPasteAndEnterToPowerShell(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()
	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	_, _ = model.Update(tea.PasteMsg{Content: "echo __SPLIT_MODEL_INPUT_OK__"})
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	deadline := time.After(10 * time.Second)
	for {
		if strings.Contains(ansi.Strip(model.View().Content), "__SPLIT_MODEL_INPUT_OK__") {
			return
		}
		select {
		case event := <-model.TerminalEvents():
			model.ApplyTerminalEvents([]terminal.Event{event})
		case <-deadline:
			t.Fatalf("PowerShell did not receive model input; mode=%v content=%q", model.mode, ansi.Strip(model.View().Content))
		}
	}
}
func TestPrefixCreatesPlainTerminalPaneAndQuitDetaches(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	before := len(model.active().root.Leaves())
	model.mode = modePrefix
	_, _ = model.handlePrefixKey(tea.KeyPressMsg(tea.Key{Code: 'v'}))
	if got := len(model.active().root.Leaves()); got != before+1 {
		t.Fatalf("expected a new terminal pane, got %d panes", got)
	}
	if active := model.activePane(); active == nil || active.title != "PowerShell" {
		t.Fatal("splits should always create plain PowerShell terminals")
	}

	model.mode = modeNavigate
	_, _ = model.handleNavigationKey(tea.KeyPressMsg(tea.Key{Code: 'q'}))
	if !model.TakeDetachRequest() {
		t.Fatal("q should request client detachment")
	}
	if model.TakeDetachRequest() {
		t.Fatal("detach requests should be consumed once")
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

func TestThinTerminalPaneStaysWithinItsFrame(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	const width = 80
	const height = 30
	_, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	model.splitActive(layout.Columns)
	view := model.View()

	if strings.Contains(view.Content, "Agent pane too narrow") {
		t.Fatal("plain terminal panes should render their emulator at every size")
	}
	for row, line := range strings.Split(view.Content, "\n") {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("row %d escaped the viewport: got width %d", row, got)
		}
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

func TestPaneContextMenuCreatesPlainTerminalWithMouse(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	const width = 100
	const height = 30
	_, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	active := model.active()
	terminalRect := active.root.Rects(model.workspaceRect())[active.activePane]

	model.handleMouseClick(tea.Mouse{
		X: terminalRect.X + terminalRect.Width/2, Y: terminalRect.Y + terminalRect.Height/2,
		Button: tea.MouseLeft,
	})
	model.handleMouseClick(tea.Mouse{
		X: terminalRect.X + terminalRect.Width - 2, Y: terminalRect.Y + terminalRect.Height - 2,
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
		t.Fatal("context menu is missing pane actions")
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

	before := len(active.root.Leaves())
	model.handlePaneContextMenuClick(tea.Mouse{
		X: geometry.x + 2, Y: geometry.y + 1, Button: tea.MouseLeft,
	})
	if model.contextMenu.open {
		t.Fatal("context menu should close after creating a split")
	}
	if got := len(active.root.Leaves()); got != before+1 {
		t.Fatalf("expected mouse action to create a pane, got %d panes", got)
	}
	if item := model.activePane(); item == nil || item.title != "PowerShell" {
		t.Fatal("mouse splits should create plain PowerShell terminals")
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

func TestSidebarShowsOneLiveRowPerDetectedAgent(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	firstPane := model.active().activePane
	model.splitActive(layout.Columns)
	secondPane := model.active().activePane
	model.agents[firstPane] = agent.State{
		PaneID: firstPane,
		PID:    101,
		Kind:   agent.KindCodex,
		Status: agent.StatusWorking,
	}
	model.agents[secondPane] = agent.State{
		PaneID: secondPane,
		PID:    102,
		Kind:   agent.KindClaude,
		Status: agent.StatusBlocked,
	}

	plain := ansi.Strip(model.renderSidebar(sidebarWidth, 30))
	if !strings.Contains(plain, "Codex · working") {
		t.Fatalf("sidebar is missing the working Codex row: %q", plain)
	}
	if !strings.Contains(plain, "! Claude · blocked") {
		t.Fatalf("sidebar is missing the blocked Claude row: %q", plain)
	}

	codex := model.agents[firstPane]
	codex.Status = agent.StatusFinished
	model.agents[firstPane] = codex
	claude := model.agents[secondPane]
	claude.Status = agent.StatusExited
	model.agents[secondPane] = claude
	plain = ansi.Strip(model.renderSidebar(sidebarWidth, 30))
	if !strings.Contains(plain, "✓ Codex · done") {
		t.Fatalf("sidebar is missing the completed Codex row: %q", plain)
	}
	if !strings.Contains(plain, "× Claude · exited") {
		t.Fatalf("sidebar is missing the exited Claude row: %q", plain)
	}
}

func TestTerminalInputMarksAgentWorkingAndInterrupted(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	paneID := model.active().activePane
	model.agents[paneID] = agent.State{
		PaneID: paneID,
		PID:    101,
		Kind:   agent.KindClaude,
		Status: agent.StatusIdle,
	}
	model.mode = modeTerminal

	_, _ = model.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if current := model.agents[paneID]; current.Status != agent.StatusWorking {
		t.Fatalf("Enter should immediately mark a detected agent working: %#v", current)
	}
	_, _ = model.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if current := model.agents[paneID]; current.Status != agent.StatusInterrupted {
		t.Fatalf("Esc should immediately mark a working agent interrupted: %#v", current)
	}

	plain := ansi.Strip(model.renderSidebar(sidebarWidth, 30))
	if !strings.Contains(plain, "× Claude · interrup") {
		t.Fatalf("sidebar should show the red interruption marker: %q", plain)
	}
}

func TestClickingSidebarAgentFocusesItsTerminal(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	firstPane := model.active().activePane
	model.splitActive(layout.Columns)
	secondPane := model.active().activePane
	model.active().activePane = firstPane
	model.agents[secondPane] = agent.State{
		PaneID: secondPane,
		PID:    102,
		Kind:   agent.KindClaude,
		Status: agent.StatusIdle,
	}

	agentRow := -1
	for index, row := range model.sidebarRows() {
		if row.kind == sidebarAgentRow && row.paneID == secondPane {
			agentRow = index
			break
		}
	}
	if agentRow < 0 {
		t.Fatal("expected a sidebar row for the detected agent")
	}
	model.handleMouseClick(tea.Mouse{
		X:      2,
		Y:      sidebarProjectStart + agentRow,
		Button: tea.MouseLeft,
	})
	if model.active().activePane != secondPane {
		t.Fatalf("agent row focused %q, want %q", model.active().activePane, secondPane)
	}
	if model.mode != modeTerminal || model.focus != focusPanes {
		t.Fatal("clicking an agent row should enter its live terminal")
	}
}
