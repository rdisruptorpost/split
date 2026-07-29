package app

import (
	"path/filepath"
	"testing"
	"time"

	"split/internal/agent"
	"split/internal/layout"
	"split/internal/state"
)

type fakeCodexSessionResolver struct {
	sessionID string
	pid       uint32
}

func (resolver *fakeCodexSessionResolver) Resolve(
	pid uint32,
	_ time.Time,
) (string, bool, error) {
	resolver.pid = pid
	return resolver.sessionID, resolver.sessionID != "", nil
}

func (*fakeCodexSessionResolver) Close() error {
	return nil
}

func TestCodexProcessCorrelationPersistsBeforeFirstPrompt(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	model := newModel(root)
	model.store = store
	_ = model.codexSessions.Close()
	resolver := &fakeCodexSessionResolver{
		sessionID: "019f3507-d97c-7380-ba94-6eae6bc92701",
	}
	model.codexSessions = resolver

	paneID := "pane-1"
	model.panes[paneID] = &pane{
		id:    paneID,
		title: "PowerShell",
		kind:  paneTerminal,
		cwd:   root,
	}
	model.tabs = []*tab{{
		id:         "tab-1",
		title:      "Prism Lab",
		rootPath:   root,
		root:       layout.Leaf(paneID),
		activePane: paneID,
	}}
	if err := model.saveState(); err != nil {
		t.Fatal(err)
	}
	if !model.captureCodexSessions(map[string]agent.State{
		paneID: {
			PaneID: paneID,
			PID:    26712,
			Kind:   agent.KindCodex,
			Status: agent.StatusIdle,
		},
	}, time.Unix(1_000, 0), false) {
		t.Fatal("expected exact Codex session correlation to be persisted")
	}
	if resolver.pid != 26712 {
		t.Fatalf("resolver PID = %d, want 26712", resolver.pid)
	}
	if item := model.panes[paneID]; item.resumeProvider != "codex" ||
		item.resumeSessionID != resolver.sessionID {
		t.Fatalf("in-memory pane binding = %#v", item)
	}

	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	saved := snapshot.Projects[0].Panes[0]
	if saved.AgentProvider != "codex" ||
		saved.AgentSessionID != resolver.sessionID ||
		filepath.Clean(saved.AgentDirectory) != filepath.Clean(root) {
		t.Fatalf("persisted pane binding = %#v", saved)
	}
	model.Close()
}
