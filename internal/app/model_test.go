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
		t.Fatal("view does not contain the split graphic")
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

func TestSidebarUsesPaneFrameAsSingleDivider(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	const width = 100
	const height = 30
	_, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	lines := strings.Split(ansi.Strip(model.View().Content), "\n")
	boundary := model.effectiveSidebarWidth()
	row := []rune(lines[1])

	if got := row[boundary-1]; got == '\u2502' {
		t.Fatal("sidebar should not draw a redundant right border")
	}
	if got := row[boundary]; got != '\u2502' {
		t.Fatalf("pane frame should supply the single divider, got %q", got)
	}
}

func TestActiveProjectRowUsesOneContinuousBackgroundStyle(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	row := model.renderProjectSidebarRow(0, sidebarWidth)
	plain := ansi.Strip(row)
	if got := ansi.StringWidth(row); got != sidebarWidth {
		t.Fatalf("active project row width = %d, want %d", got, sidebarWidth)
	}
	if want := styles.activeSession.Render(plain); row != want {
		t.Fatalf("active project row contains nested resets that break its background\nwant: %q\n got: %q", want, row)
	}
}

func TestRightClickProjectMenuOpensRenameDialog(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()
	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model.mode = modeTerminal
	model.focus = focusPanes

	_, _ = model.Update(tea.MouseClickMsg(tea.Mouse{
		X: 2, Y: sidebarProjectStart, Button: tea.MouseRight,
	}))
	if !model.projectMenu.open || model.projectMenu.targetProject != 0 {
		t.Fatalf("right-click should open the project menu for project zero: %#v", model.projectMenu)
	}
	if model.mode != modeNavigate || model.focus != focusSidebar {
		t.Fatal("right-clicking the sidebar should leave terminal-input mode")
	}
	items := model.projectContextMenuItems()
	if len(items) != 2 || items[0].label != "Rename project" || items[1].label != "Close project" {
		t.Fatalf("project menu items = %#v", items)
	}
	if items[1].enabled {
		t.Fatal("Close project should be disabled for the final project")
	}
	menuView := model.View()
	menuPlain := ansi.Strip(menuView.Content)
	if !strings.Contains(menuPlain, "Rename project") || !strings.Contains(menuPlain, "Close project") {
		t.Fatalf("project menu is incomplete: %q", menuPlain)
	}
	if menuView.MouseMode != tea.MouseModeAllMotion || menuView.Cursor != nil {
		t.Fatal("project menu should enable hover tracking and hide the terminal cursor")
	}

	menuGeometry := model.projectContextMenuGeometry()
	_, _ = model.Update(tea.MouseClickMsg(tea.Mouse{
		X: menuGeometry.x + 2, Y: menuGeometry.y + 1, Button: tea.MouseLeft,
	}))
	if model.projectMenu.open || !model.renameDialog.open || model.renameDialog.projectIndex != 0 {
		t.Fatalf("Rename project should open the modal: menu=%#v dialog=%#v", model.projectMenu, model.renameDialog)
	}
	if !model.renameDialog.selectAll {
		t.Fatal("the existing project name should start selected")
	}
	view := model.View()
	plain := ansi.Strip(view.Content)
	if !strings.Contains(plain, "Rename project") ||
		!strings.Contains(plain, "Project name") ||
		!strings.Contains(plain, "Cancel") ||
		!strings.Contains(plain, "Save") {
		t.Fatalf("rename dialog is incomplete: %q", plain)
	}
	lines := strings.Split(view.Content, "\n")
	if len(lines) != 30 {
		t.Fatalf("rename overlay height = %d, want 30", len(lines))
	}
	for row, line := range lines {
		if got := ansi.StringWidth(line); got != 100 {
			t.Fatalf("rename overlay row %d width = %d, want 100", row, got)
		}
	}

	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "release workspace", Code: 'r'}))
	if got := string(model.renameDialog.value); got != "release workspace" {
		t.Fatalf("typing should replace the selected name, got %q", got)
	}
	if cursor := model.View().Cursor; cursor == nil || !cursor.Blink || cursor.Shape != tea.CursorBar {
		t.Fatalf("editing should expose a blinking bar cursor: %#v", cursor)
	}

	renameGeometry := model.projectRenameDialogGeometry()
	_, _ = model.Update(tea.MouseMotionMsg(tea.Mouse{
		X: renameGeometry.saveX + 1, Y: renameGeometry.buttonY,
	}))
	if model.renameDialog.hovered != projectRenameSave {
		t.Fatal("hovering Save should highlight it")
	}
	_, _ = model.Update(tea.MouseClickMsg(tea.Mouse{
		X: renameGeometry.saveX + 1, Y: renameGeometry.buttonY, Button: tea.MouseLeft,
	}))
	if model.renameDialog.open {
		t.Fatal("clicking Save should close the rename dialog")
	}
	if model.mode != modeNavigate || model.focus != focusSidebar {
		t.Fatal("closing rename should preserve sidebar navigation focus")
	}
	if got := model.tabs[0].title; got != "release workspace" {
		t.Fatalf("project was renamed to %q", got)
	}
	if got := model.View().WindowTitle; got != "split \u2014 release workspace" {
		t.Fatalf("active window title should follow the project name, got %q", got)
	}

	_, _ = model.Update(tea.MouseClickMsg(tea.Mouse{
		X: 2, Y: sidebarProjectStart, Button: tea.MouseRight,
	}))
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "discarded", Code: 'd'}))
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if model.renameDialog.open || model.tabs[0].title != "release workspace" {
		t.Fatal("Escape should cancel a pending rename")
	}
}

func TestProjectContextMenuClosesEveryPaneInTargetProject(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()
	_, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 32})

	model.splitActive(layout.Columns)
	closedProject := model.tabs[0]
	closedPaneIDs := append([]string(nil), closedProject.root.Leaves()...)
	closedSessions := make(map[string]*terminal.Session, len(closedPaneIDs))
	for _, paneID := range closedPaneIDs {
		closedSessions[paneID] = model.panes[paneID].session
	}
	model.agents[closedPaneIDs[0]] = agent.State{
		PaneID: closedPaneIDs[0], PID: 101, Kind: agent.KindCodex, Status: agent.StatusIdle,
	}
	model.newProject()
	survivingProject := model.tabs[1]

	_, _ = model.Update(tea.MouseClickMsg(tea.Mouse{
		X: 2, Y: sidebarProjectStart, Button: tea.MouseRight,
	}))
	items := model.projectContextMenuItems()
	if len(items) < 2 || !items[1].enabled {
		t.Fatal("Close project should be enabled when another project exists")
	}
	geometry := model.projectContextMenuGeometry()
	_, _ = model.Update(tea.MouseMotionMsg(tea.Mouse{
		X: geometry.x + 2, Y: geometry.y + 2,
	}))
	if model.projectMenu.selected != 1 {
		t.Fatal("hovering Close project should select it")
	}
	_, _ = model.Update(tea.MouseClickMsg(tea.Mouse{
		X: geometry.x + 2, Y: geometry.y + 2, Button: tea.MouseLeft,
	}))

	if model.projectMenu.open || len(model.tabs) != 1 || model.tabs[0] != survivingProject {
		t.Fatalf("target project was not removed cleanly: menu=%#v projects=%#v", model.projectMenu, model.tabs)
	}
	if model.activeTab != 0 || model.mode != modeNavigate || model.focus != focusSidebar {
		t.Fatal("closing an earlier project should preserve the survivor with sidebar focus")
	}
	for _, paneID := range closedPaneIDs {
		if _, exists := model.panes[paneID]; exists {
			t.Fatalf("closed project pane %q remains in the model", paneID)
		}
		if _, exists := model.agents[paneID]; exists {
			t.Fatalf("closed project agent %q remains in the sidebar state", paneID)
		}
		if state, _ := closedSessions[paneID].State(); state == terminal.Running {
			t.Fatalf("closed project pane %q is still running", paneID)
		}
	}
}

func TestClosingActiveProjectSelectsNeighborAndKeepsFinalProject(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	firstProject := model.tabs[0]
	model.newProject()
	closedPaneID := model.active().activePane
	if !model.closeProject(1) {
		t.Fatal("active project should close while a neighbor exists")
	}
	if len(model.tabs) != 1 || model.active() != firstProject {
		t.Fatal("closing the active final row should select the previous project")
	}
	if _, exists := model.panes[closedPaneID]; exists {
		t.Fatal("active project's pane remains after close")
	}
	if model.closeProject(0) {
		t.Fatal("split must refuse to close its final project")
	}
	if len(model.tabs) != 1 || model.active() != firstProject {
		t.Fatal("refusing the final close should leave the project intact")
	}
}
func TestSidebarBackgroundClickLeavesTerminalModeAndQDetaches(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()
	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model.mode = modeTerminal
	model.focus = focusPanes

	model.handleMouseClick(tea.Mouse{
		X: 2, Y: sidebarProjectStart - 1, Button: tea.MouseLeft,
	})
	if model.mode != modeNavigate || model.focus != focusSidebar {
		t.Fatal("clicking blank sidebar space should enter sidebar navigation focus")
	}
	if model.View().Cursor != nil {
		t.Fatal("sidebar focus should hide the PowerShell cursor")
	}
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	if !model.TakeDetachRequest() {
		t.Fatal("q after clicking the sidebar should detach instead of reaching PowerShell")
	}
}
func TestProjectRenameRejectsBlankNames(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	model.openProjectRenameDialog(0)
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !model.renameDialog.open || model.renameDialog.errorMessage == "" {
		t.Fatal("a blank project name should keep the dialog open with an error")
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "Project name cannot be empty") {
		t.Fatal("blank-name validation should be visible in the dialog")
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
func TestMouseSelectionCopiesWithControlCAndRightClick(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()
	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	const marker = "__SPLIT_MOUSE_SELECTION_OK__"
	_, _ = model.Update(tea.PasteMsg{Content: "Write-Output " + marker})
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	item := model.activePane()
	if item == nil || item.session == nil {
		t.Fatal("active PowerShell session is unavailable")
	}
	markerRow, markerColumn := -1, -1
	deadline := time.After(10 * time.Second)
	for markerRow < 0 {
		plain := ansi.Strip(item.session.Render())
		if strings.Count(plain, marker) >= 2 {
			for row, line := range strings.Split(plain, "\n") {
				if column := strings.LastIndex(line, marker); column >= 0 {
					markerRow, markerColumn = row, column
				}
			}
		}
		if markerRow >= 0 {
			break
		}
		select {
		case event := <-model.TerminalEvents():
			model.ApplyTerminalEvents([]terminal.Event{event})
		case <-deadline:
			t.Fatalf("PowerShell did not render selectable marker: %q", plain)
		}
	}

	rect := model.active().root.Rects(model.workspaceRect())[item.id]
	inner, ok := paneInnerRect(rect)
	if !ok || markerColumn+len(marker) > inner.Width || markerRow >= inner.Height {
		t.Fatalf("marker is outside pane body: rect=%#v row=%d column=%d", inner, markerRow, markerColumn)
	}
	start := tea.Mouse{X: inner.X + markerColumn, Y: inner.Y + markerRow, Button: tea.MouseLeft}
	end := tea.Mouse{X: start.X + len(marker) - 1, Y: start.Y, Button: tea.MouseLeft}
	selectMarker := func() {
		_, _ = model.Update(tea.MouseClickMsg(start))
		_, _ = model.Update(tea.MouseMotionMsg(end))
		_, _ = model.Update(tea.MouseReleaseMsg(end))
		if got, selected := item.session.SelectedText(); !selected || got != marker {
			t.Fatalf("mouse selected %q, %v; want %q, true", got, selected, marker)
		}
		if model.View().Cursor != nil {
			t.Fatal("selected text should hide the terminal cursor")
		}
	}

	selectMarker()
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if copied, ok := model.TakeClipboardRequest(); !ok || copied != marker {
		t.Fatalf("Ctrl+C clipboard request = %q, %v; want %q, true", copied, ok, marker)
	}
	if _, ok := model.TakeClipboardRequest(); ok {
		t.Fatal("clipboard requests should be consumed exactly once")
	}
	if item.session.HasSelection() {
		t.Fatal("copying should clear the terminal selection")
	}

	selectMarker()
	_, _ = model.Update(tea.MouseClickMsg(tea.Mouse{X: end.X, Y: end.Y, Button: tea.MouseRight}))
	if model.contextMenu.open {
		t.Fatal("right-clicking a selection should copy instead of opening the pane menu")
	}
	if copied, ok := model.TakeClipboardRequest(); !ok || copied != marker {
		t.Fatalf("right-click clipboard request = %q, %v; want %q, true", copied, ok, marker)
	}

	_, _ = model.Update(tea.MouseClickMsg(tea.Mouse{X: end.X, Y: end.Y, Button: tea.MouseRight}))
	if !model.contextMenu.open {
		t.Fatal("right-click without a selection should retain the pane context menu")
	}
}
func TestControlZOwnsPrefixWhileControlBPassesThroughTerminalMode(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()
	model.mode = modeTerminal
	model.focus = focusPanes

	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	if model.mode != modeTerminal {
		t.Fatalf("Ctrl+B was intercepted as split mode %v", model.mode)
	}

	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModCtrl}))
	if model.mode != modePrefix || model.modeBeforePrefix != modeTerminal {
		t.Fatalf("Ctrl+Z did not open the terminal prefix: mode=%v previous=%v",
			model.mode, model.modeBeforePrefix)
	}
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModCtrl}))
	if model.mode != modeTerminal {
		t.Fatalf("double Ctrl+Z did not return to terminal mode: %v", model.mode)
	}

	model.mode = modeNavigate
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModCtrl}))
	if model.mode != modePrefix || model.modeBeforePrefix != modeNavigate {
		t.Fatalf("Ctrl+Z did not open the navigation prefix: mode=%v previous=%v",
			model.mode, model.modeBeforePrefix)
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

func TestSplitPreservesManualAncestorRatioAndActivePaneCanMove(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	model.splitActive(layout.Columns)

	active := model.active()
	originalPaneID := active.root.First.PaneID
	targetPaneID := active.root.Second.PaneID
	active.root.Ratio = 0.68
	beforeRatio := active.root.Ratio
	beforeRects := active.root.Rects(model.workspaceRect())
	beforeOriginal := beforeRects[originalPaneID]
	beforeTarget := beforeRects[targetPaneID]

	model.splitActive(layout.Rows)

	if got := len(active.root.Leaves()); got != 3 {
		t.Fatalf("expected three panes, got %d", got)
	}
	if active.root.Ratio != beforeRatio {
		t.Fatalf("ancestor ratio changed after split: got %f, want %f", active.root.Ratio, beforeRatio)
	}
	if active.root.Second.Ratio != 0.5 {
		t.Fatalf("new child split ratio = %f, want 0.5", active.root.Second.Ratio)
	}

	rects := active.root.Rects(model.workspaceRect())
	if got := rects[originalPaneID]; got != beforeOriginal {
		t.Fatalf("unaffected sibling moved: before=%#v after=%#v", beforeOriginal, got)
	}
	oldTargetAfter := rects[targetPaneID]
	activePaneID := active.activePane
	newPane := rects[activePaneID]
	if oldTargetAfter.X != beforeTarget.X || newPane.X != beforeTarget.X ||
		oldTargetAfter.Width != beforeTarget.Width || newPane.Width != beforeTarget.Width ||
		oldTargetAfter.Y != beforeTarget.Y ||
		oldTargetAfter.Height+layout.Gap+newPane.Height != beforeTarget.Height {
		t.Fatalf(
			"new split did not stay inside the focused pane: before=%#v old=%#v new=%#v",
			beforeTarget,
			oldTargetAfter,
			newPane,
		)
	}

	before := rects[activePaneID]
	model.swapActivePane(layout.Left)
	after := active.root.Rects(model.workspaceRect())[activePaneID]
	if after.X >= before.X {
		t.Fatalf("expected active pane to move left: before=%#v after=%#v", before, after)
	}
}

func TestClosePanePreservesManualAncestorRatio(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	model.splitActive(layout.Columns)

	active := model.active()
	unaffectedPaneID := active.root.First.PaneID
	survivingPaneID := active.root.Second.PaneID
	active.root.Ratio = 0.68
	beforeRatio := active.root.Ratio
	beforeRects := active.root.Rects(model.workspaceRect())
	beforeUnaffected := beforeRects[unaffectedPaneID]
	beforeTarget := beforeRects[survivingPaneID]

	model.splitActive(layout.Rows)
	closedPaneID := active.activePane
	model.closeActivePane()

	if got := len(active.root.Leaves()); got != 2 {
		t.Fatalf("expected two panes after close, got %d", got)
	}
	if active.root.Ratio != beforeRatio {
		t.Fatalf("ancestor ratio changed after close: got %f, want %f", active.root.Ratio, beforeRatio)
	}
	rects := active.root.Rects(model.workspaceRect())
	if got := rects[unaffectedPaneID]; got != beforeUnaffected {
		t.Fatalf("unaffected sibling moved: before=%#v after=%#v", beforeUnaffected, got)
	}
	if got := rects[survivingPaneID]; got != beforeTarget {
		t.Fatalf("local sibling did not absorb the closed pane: before=%#v after=%#v", beforeTarget, got)
	}
	if _, exists := model.panes[closedPaneID]; exists {
		t.Fatalf("closed pane %q is still registered", closedPaneID)
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
	if !strings.Contains(view.Content, "split right") || !strings.Contains(view.Content, "Close pane") {
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
func TestPaneContextMenuRenamesTerminalAndOverridesAgentLabel(t *testing.T) {
	model := New(t.TempDir())
	defer model.Close()

	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	paneID := model.active().activePane
	rect := model.active().root.Rects(model.workspaceRect())[paneID]
	model.handleMouseClick(tea.Mouse{
		X: rect.X + 2, Y: rect.Y + 2, Button: tea.MouseLeft,
	})
	model.handleMouseClick(tea.Mouse{
		X: rect.X + 2, Y: rect.Y + 2, Button: tea.MouseRight,
	})

	menuPlain := ansi.Strip(model.View().Content)
	if !strings.Contains(menuPlain, "Rename terminal") {
		t.Fatalf("pane menu is missing terminal rename: %q", menuPlain)
	}
	renameIndex := -1
	for index, item := range model.paneContextMenuItems() {
		if item.action == paneMenuRename {
			renameIndex = index
			break
		}
	}
	if renameIndex < 0 {
		t.Fatal("Rename terminal action is absent from the pane menu model")
	}
	menuGeometry := model.paneContextMenuGeometry()
	_, _ = model.Update(tea.MouseMotionMsg(tea.Mouse{
		X: menuGeometry.x + 2, Y: menuGeometry.y + 1 + renameIndex,
	}))
	if model.contextMenu.selected != renameIndex {
		t.Fatal("hovering Rename terminal should select it")
	}
	_, _ = model.Update(tea.MouseClickMsg(tea.Mouse{
		X: menuGeometry.x + 2, Y: menuGeometry.y + 1 + renameIndex, Button: tea.MouseLeft,
	}))
	if model.contextMenu.open || !model.renameDialog.open ||
		model.renameDialog.target != renameTargetPane || model.renameDialog.paneID != paneID {
		t.Fatalf("Rename terminal should open the pane rename dialog: menu=%#v dialog=%#v", model.contextMenu, model.renameDialog)
	}
	dialogPlain := ansi.Strip(model.View().Content)
	if !strings.Contains(dialogPlain, "Rename terminal") ||
		!strings.Contains(dialogPlain, "Terminal name") {
		t.Fatalf("terminal rename dialog is incomplete: %q", dialogPlain)
	}

	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !model.renameDialog.open ||
		!strings.Contains(ansi.Strip(model.View().Content), "Terminal name cannot be empty") {
		t.Fatal("a blank terminal name should keep the dialog open with a visible error")
	}

	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "API worker", Code: 'A'}))
	_, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if model.renameDialog.open {
		t.Fatal("saving a valid terminal name should close the dialog")
	}
	if got := model.panes[paneID].title; got != "API worker" {
		t.Fatalf("terminal title = %q, want API worker", got)
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "API worker") {
		t.Fatal("renamed terminal should use its custom pane-frame title")
	}

	model.agents[paneID] = agent.State{
		PaneID: paneID,
		PID:    101,
		Kind:   agent.KindCodex,
		Status: agent.StatusWorking,
	}
	sidebarPlain := ansi.Strip(model.renderSidebar(sidebarWidth, 30))
	if !strings.Contains(sidebarPlain, "API worker") {
		t.Fatalf("agent row should use the custom terminal name: %q", sidebarPlain)
	}
	if strings.Contains(sidebarPlain, "Codex") {
		t.Fatalf("detected agent name should be overridden after a pane rename: %q", sidebarPlain)
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

func TestClickingSidebarAgentSelectsPaneButKeepsSidebarFocus(t *testing.T) {
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
	model.mode = modeTerminal

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
		t.Fatalf("agent row selected %q, want %q", model.active().activePane, secondPane)
	}
	if model.mode != modeNavigate || model.focus != focusSidebar {
		t.Fatal("clicking an agent row should keep keyboard focus in the sidebar")
	}
}

func TestAgentResumeCommandUsesOnlyExactProviderSessionIDs(t *testing.T) {
	tests := []struct {
		provider  string
		sessionID string
		want      string
		ok        bool
	}{
		{provider: "codex", sessionID: "019fa900-e81f-7061-b8c3-948b6c301456", want: "codex resume '019fa900-e81f-7061-b8c3-948b6c301456'", ok: true},
		{provider: "claude", sessionID: "64d41bea-ae7a-447e-99e0-32d44d2dda90", want: "claude --resume '64d41bea-ae7a-447e-99e0-32d44d2dda90'", ok: true},
		{provider: "codex", ok: false},
		{provider: "unknown", sessionID: "session-1", ok: false},
		{provider: "codex", sessionID: "-dangerous", ok: false},
		{provider: "claude", sessionID: "bad\ncommand", ok: false},
	}
	for _, test := range tests {
		got, ok := agentResumeCommand(test.provider, test.sessionID)
		if got != test.want || ok != test.ok {
			t.Fatalf("agentResumeCommand(%q, %q) = %q, %v; want %q, %v",
				test.provider, test.sessionID, got, ok, test.want, test.ok)
		}
	}
}
