package agent

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexSessionResolverFindsFreshRootThreadForProcess(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "logs_2.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		ts_nanos INTEGER NOT NULL DEFAULT 0,
		process_uuid TEXT,
		thread_id TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`CREATE INDEX idx_logs_ts ON logs(ts DESC, ts_nanos DESC, id DESC)`,
	); err != nil {
		t.Fatal(err)
	}
	observedAt := time.Unix(1_000, 0)
	const pid = uint32(4_294_967_294)
	rootID := "019f3507-d97c-7380-ba94-6eae6bc92701"
	subagentID := "019f3507-d97c-7380-ba94-6eae6bc92702"
	rows := []struct {
		timestamp int64
		process   string
		threadID  string
	}{
		{980, "pid:4294967294:stale", rootID},
		{999, "pid:7:other", subagentID},
		{999, "pid:4294967294:current", "not-a-session"},
		{1_000, "pid:4294967294:current", rootID},
		{1_001, "pid:4294967294:current", subagentID},
	}
	for _, row := range rows {
		if _, err := db.Exec(
			`INSERT INTO logs (ts, process_uuid, thread_id) VALUES (?, ?, ?)`,
			row.timestamp,
			row.process,
			row.threadID,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	resolver := NewCodexSessionResolverAt(home)
	t.Cleanup(func() { _ = resolver.Close() })
	got, found, err := resolver.Resolve(pid, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got != rootID {
		t.Fatalf("Resolve() = %q, %v; want %q, true", got, found, rootID)
	}
	if got, found, err := resolver.Resolve(99, observedAt); err != nil || found || got != "" {
		t.Fatalf("unrelated PID Resolve() = %q, %v, %v", got, found, err)
	}
}

func TestLatestCodexLogDatabaseUsesNewestSchemaVersion(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"logs_2.sqlite", "logs_11.sqlite", "state_5.sqlite"} {
		if err := os.WriteFile(filepath.Join(home, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path, err := latestCodexLogDatabase(home)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "logs_11.sqlite" {
		t.Fatalf("latestCodexLogDatabase() = %q", path)
	}
}
