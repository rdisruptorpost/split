package state

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStoreRoundTripPreservesProjectOrderLayoutAndSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	snapshot := Snapshot{
		ActiveProjectID:   "project-b",
		SidebarVisible:    false,
		NextProjectNumber: 7,
		Projects: []Project{
			{
				ID: "project-a", Name: "alpha", RootPath: `C:\work\alpha`, ActivePaneID: "pane-a",
				LayoutJSON: []byte(`{"pane_id":"pane-a"}`),
				Panes: []Pane{{
					ID: "pane-a", Profile: "codex", Title: "Codex", WorkingDirectory: `C:\work\alpha`, ProviderSessionID: "thr_alpha",
				}},
			},
			{
				ID: "project-b", Name: "beta", RootPath: `C:\work\beta`, ActivePaneID: "pane-b",
				LayoutJSON: []byte(`{"pane_id":"pane-b"}`),
				Panes: []Pane{{
					ID: "pane-b", Profile: "powershell", Title: "PowerShell", WorkingDirectory: `C:\work\beta`,
				}},
			},
		},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, snapshot) {
		t.Fatalf("round trip mismatch\nwant: %#v\n got: %#v", snapshot, loaded)
	}
}

func TestSessionBindingSurvivesSnapshotWritesAndCanArriveEarly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.BindSession("pane-1", "codex", "thr_first", `C:\work`); err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{
		ActiveProjectID: "project-1", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []Project{{
			ID: "project-1", Name: "work", RootPath: `C:\work`, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes:      []Pane{{ID: "pane-1", Profile: "codex", Title: "Codex", WorkingDirectory: `C:\work`}},
		}},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Projects[0].Panes[0].ProviderSessionID; got != "thr_first" {
		t.Fatalf("early hook binding was lost: %q", got)
	}

	if err := store.BindSession("pane-1", "codex", "thr_resumed", `C:\work`); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Projects[0].Panes[0].ProviderSessionID; got != "thr_resumed" {
		t.Fatalf("newer hook binding was overwritten by an old snapshot: %q", got)
	}
}

func TestBindSessionFileWhileApplicationStoreIsOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := BindSessionFile(path, "pane-1", "codex", "thread-1", `C:\work`); err != nil {
		t.Fatal(err)
	}
}

func TestBindSessionFileWaitsForApplicationWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO session_bindings
		(pane_id, provider, session_id, working_directory, updated_at)
		VALUES ('other-pane', 'codex', 'other-thread', 'C:\work', 1)`); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- BindSessionFile(path, "pane-1", "codex", "thread-1", `C:\work`)
	}()
	time.Sleep(100 * time.Millisecond)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestQueuedSessionEventImportsWhenStoreOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{
		ActiveProjectID: "project-1", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []Project{{
			ID: "project-1", Name: "work", RootPath: `C:\work`, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes:      []Pane{{ID: "pane-1", Profile: "codex", Title: "Codex", WorkingDirectory: `C:\work`}},
		}},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := QueueSessionEvent(path, "pane-1", "codex", "thread-spooled", `C:\work`); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(sessionEventsDirectory(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one queued event, entries=%d err=%v", len(entries), err)
	}

	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Projects[0].Panes[0].ProviderSessionID; got != "thread-spooled" {
		t.Fatalf("queued session was not imported: %q", got)
	}
	entries, err = os.ReadDir(sessionEventsDirectory(path))
	if err != nil || len(entries) != 0 {
		t.Fatalf("imported event was not removed, entries=%d err=%v", len(entries), err)
	}
}

func TestSessionLaunchClaimMapsSanitizedHookToPane(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{
		ActiveProjectID: "project-1", SidebarVisible: true, NextProjectNumber: 2,
		Projects: []Project{{
			ID: "project-1", Name: "work", RootPath: `C:\work`, ActivePaneID: "pane-1",
			LayoutJSON: []byte(`{"pane_id":"pane-1"}`),
			Panes:      []Pane{{ID: "pane-1", Profile: "codex", Title: "Codex", WorkingDirectory: `C:\work`}},
		}},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := RegisterSessionLaunch(path, "pane-1", "codex", `C:\work`); err != nil {
		t.Fatal(err)
	}
	if !HasPendingSessionLaunch(path, "codex") {
		t.Fatal("registered Codex launch was not found")
	}
	matched, err := ClaimSessionEvent(path, "codex", "019fa784-9e07-76e0-b202-851e53e4ae0b", `C:\work`, "", "startup")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("session hook did not claim its pane launch")
	}
	if HasPendingSessionLaunch(path, "codex") {
		t.Fatal("claimed launch was not consumed")
	}

	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Projects[0].Panes[0].ProviderSessionID; got != "019fa784-9e07-76e0-b202-851e53e4ae0b" {
		t.Fatalf("claimed session was not imported: %q", got)
	}
}
