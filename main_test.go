package main

import (
	"os"
	"path/filepath"
	"testing"

	"split/internal/state"
)

func TestMalformedLegacyHookCommandRemainsSilent(t *testing.T) {
	oldArguments := os.Args
	os.Args = []string{"split", "hook", "session-start", "codex"}
	defer func() { os.Args = oldArguments }()

	if err := run(); err != nil {
		t.Fatalf("malformed legacy provider hooks should remain harmless: %v", err)
	}
}

func TestServerCommandRejectsUnsupportedArguments(t *testing.T) {
	for _, arguments := range [][]string{nil, {"run"}, {"stop", "extra"}, {"unknown"}} {
		if err := runServerCommand(arguments); err == nil {
			t.Fatalf("expected usage error for %#v", arguments)
		}
	}
}

func TestClaudeProviderUsageHookWritesSQLiteCache(t *testing.T) {
	t.Setenv("SPLIT_ENV", "1")
	t.Setenv("SPLIT_PANE_ID", "pane-claude")
	statePath := filepath.Join(t.TempDir(), "state.db")

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
		reader.Close()
	}()
	if _, err := writer.WriteString(`{
		"rate_limits":{"seven_day":{"used_percentage":56,"resets_at":1900000000}}
	}`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := runProviderUsageHook("claude", statePath); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	values, err := store.LoadProviderUsage()
	if err != nil {
		t.Fatal(err)
	}
	if got := values["claude"].UsedPercent; got != 56 {
		t.Fatalf("stored Claude used percent = %v, want 56", got)
	}
}
