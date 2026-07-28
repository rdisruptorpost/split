package agent

import (
	"testing"
	"time"
)

func TestTrackerDiscoversCodexAndKeepsCompletionTransition(t *testing.T) {
	processes := []processInfo{
		{pid: 10, parentPID: 1, name: "powershell.exe"},
		{pid: 20, parentPID: 10, name: "codex.exe"},
		{pid: 21, parentPID: 20, name: "node_repl.exe"},
	}
	tracker := newTracker(
		func() ([]processInfo, error) { return processes, nil },
		func(uint32) string { return "" },
	)
	now := time.Unix(1_000, 0)
	target := Target{PaneID: "pane-1", RootPID: 10, TerminalUp: true, LastOutput: now}

	state := tracker.Refresh([]Target{target}, now)[target.PaneID]
	if state.Kind != KindCodex || state.Status != StatusLoading || state.PID != 20 {
		t.Fatalf("unexpected initial state: %#v", state)
	}

	target.Screen = "• Working (2s • esc to interrupt)"
	state = tracker.Refresh([]Target{target}, now.Add(time.Second))[target.PaneID]
	if state.Status != StatusWorking {
		t.Fatalf("working screen was not detected: %#v", state)
	}

	target.Screen = "› Ask Codex anything"
	target.Title = "Codex"
	state = tracker.Refresh([]Target{target}, now.Add(2*time.Second))[target.PaneID]
	if state.Status != StatusFinished {
		t.Fatalf("working-to-idle should become finished: %#v", state)
	}

	processes = processes[:1]
	state = tracker.Refresh([]Target{target}, now.Add(3*time.Second))[target.PaneID]
	if state.Status != StatusExited {
		t.Fatalf("an agent process that disappeared should remain as exited: %#v", state)
	}

	if states := tracker.Refresh(nil, now.Add(4*time.Second)); len(states) != 0 {
		t.Fatalf("removing a pane should remove its tracked agent: %#v", states)
	}
}

func TestClaudeSubmissionOverridesPersistentIdlePrompt(t *testing.T) {
	processes := []processInfo{
		{pid: 10, parentPID: 1, name: "powershell.exe"},
		{pid: 20, parentPID: 10, name: "claude.exe"},
	}
	tracker := newTracker(
		func() ([]processInfo, error) { return processes, nil },
		func(uint32) string { return "" },
	)
	now := time.Unix(1_500, 0)
	target := Target{
		PaneID:     "pane-1",
		RootPID:    10,
		TerminalUp: true,
		Screen:     "\u276f Ask Claude",
		Title:      "Claude Code",
		LastOutput: now.Add(-time.Second),
	}

	state := tracker.Refresh([]Target{target}, now)[target.PaneID]
	if state.Status != StatusIdle {
		t.Fatalf("Claude prompt should initially be idle: %#v", state)
	}
	if !tracker.MarkSubmitted(target.PaneID, now.Add(time.Second)) {
		t.Fatal("Enter in a recognized Claude pane should start a working turn")
	}
	if state = tracker.snapshotStates()[target.PaneID]; state.Status != StatusWorking {
		t.Fatalf("submission should immediately show working: %#v", state)
	}

	// Claude can retain its prompt marker while its TUI redraws thinking output.
	target.LastOutput = now.Add(2500 * time.Millisecond)
	state = tracker.Refresh([]Target{target}, now.Add(3*time.Second))[target.PaneID]
	if state.Status != StatusWorking {
		t.Fatalf("recent Claude output should override the stale idle marker: %#v", state)
	}

	state = tracker.Refresh([]Target{target}, now.Add(5*time.Second))[target.PaneID]
	if state.Status != StatusFinished {
		t.Fatalf("quiet Claude prompt should complete after activity stops: %#v", state)
	}
}

func TestInterruptedTurnIsHeldThenReturnsToIdle(t *testing.T) {
	processes := []processInfo{
		{pid: 10, parentPID: 1, name: "powershell.exe"},
		{pid: 20, parentPID: 10, name: "codex.exe"},
	}
	tracker := newTracker(
		func() ([]processInfo, error) { return processes, nil },
		func(uint32) string { return "" },
	)
	now := time.Unix(1_750, 0)
	target := Target{
		PaneID:     "pane-1",
		RootPID:    10,
		TerminalUp: true,
		Screen:     ">_ Ask Codex anything",
		Title:      "Codex",
	}

	state := tracker.Refresh([]Target{target}, now)[target.PaneID]
	if state.Status != StatusIdle {
		t.Fatalf("Codex prompt should initially be idle: %#v", state)
	}
	tracker.MarkSubmitted(target.PaneID, now.Add(time.Second))
	if !tracker.MarkInterrupted(target.PaneID, now.Add(1200*time.Millisecond)) {
		t.Fatal("Esc should interrupt a working agent")
	}
	state = tracker.snapshotStates()[target.PaneID]
	if state.Status != StatusInterrupted {
		t.Fatalf("Esc should immediately show interrupted: %#v", state)
	}

	state = tracker.Refresh([]Target{target}, now.Add(2*time.Second))[target.PaneID]
	if state.Status != StatusInterrupted {
		t.Fatalf("interruption should remain visible briefly: %#v", state)
	}
	state = tracker.Refresh([]Target{target}, now.Add(4*time.Second))[target.PaneID]
	if state.Status != StatusIdle {
		t.Fatalf("interruption should return to idle, not a completion tick: %#v", state)
	}
	if tracker.MarkInterrupted(target.PaneID, now.Add(5*time.Second)) {
		t.Fatal("Esc at an idle prompt must not create a false interruption")
	}
}

func TestTrackerDetectsClaudeBlockerAndWrappedRuntime(t *testing.T) {
	processes := []processInfo{
		{pid: 10, parentPID: 1, name: "powershell.exe"},
		{pid: 20, parentPID: 10, name: "node.exe"},
	}
	tracker := newTracker(
		func() ([]processInfo, error) { return processes, nil },
		func(pid uint32) string {
			if pid == 20 {
				return `node C:\tools\node_modules\@anthropic-ai\claude-code\cli.js`
			}
			return ""
		},
	)
	now := time.Unix(2_000, 0)
	state := tracker.Refresh([]Target{{
		PaneID:     "pane-1",
		RootPID:    10,
		TerminalUp: true,
		Screen:     "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel",
	}}, now)["pane-1"]
	if state.Kind != KindClaude || state.Status != StatusBlocked {
		t.Fatalf("wrapped Claude blocker was not detected: %#v", state)
	}
}

func TestScreenDetectionOnlyUsesRecentTerminalRows(t *testing.T) {
	oldPrompt := "Do you want to proceed?\n1. Yes\nEsc to cancel\n"
	quietRows := ""
	for range 20 {
		quietRows += "ordinary transcript line\n"
	}
	if got := detectScreen(KindClaude, oldPrompt+quietRows+"❯ ", "✳ Claude Code"); got != StatusIdle {
		t.Fatalf("stale permission text should not override the live prompt, got %v", got)
	}
}

func TestDirectProcessKindIgnoresHelpers(t *testing.T) {
	if got := directProcessKind("codex.exe"); got != KindCodex {
		t.Fatalf("codex.exe = %v", got)
	}
	if got := directProcessKind("claude.exe"); got != KindClaude {
		t.Fatalf("claude.exe = %v", got)
	}
	if got := directProcessKind("node_repl.exe"); got != KindUnknown {
		t.Fatalf("node_repl.exe should not be an agent, got %v", got)
	}
}
