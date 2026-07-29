package main

import (
	"os"
	"testing"
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
