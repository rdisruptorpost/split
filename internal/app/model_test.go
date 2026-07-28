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
	if !strings.Contains(view.Content, "Command center") {
		t.Fatal("view does not contain the overview pane")
	}
	plainLines := strings.Split(ansi.Strip(view.Content), "\n")
	if !strings.Contains(plainLines[1], "Command center") {
		t.Fatal("compact pane header should begin directly below the one-row tab bar")
	}
	if !view.AltScreen {
		t.Fatal("view should use the alternate screen")
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
	if got := len(active.root.Leaves()); got != 4 {
		t.Fatalf("expected four panes, got %d", got)
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

	overviewID := active.root.First.PaneID
	overviewRect := rects[overviewID]
	model.handleMouseClick(tea.Mouse{
		X:      overviewRect.X + overviewRect.Width/2,
		Y:      overviewRect.Y + overviewRect.Height/2,
		Button: tea.MouseLeft,
	})
	if model.mode != modeNavigate || active.activePane != overviewID {
		t.Fatal("clicking an informational pane should select it without entering input mode")
	}
}
