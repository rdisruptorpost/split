package terminal

import (
	"strings"
	"testing"
)

func TestTerminalEnvironmentAppliesPerCommandOverrides(t *testing.T) {
	environment := terminalEnvironment([]string{
		"Path=C:\\Windows",
		"TERM=old",
		"SPLIT_PANE_ID=old-pane",
	}, map[string]string{
		"SPLIT_PANE_ID":  "pane-42",
		"SPLIT_PROVIDER": "codex",
	})

	values := make(map[string]string)
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[strings.ToUpper(key)] = value
		}
	}
	if values["TERM"] != "xterm-256color" || values["TERM_PROGRAM"] != "split" {
		t.Fatalf("terminal identity was not applied: %#v", values)
	}
	if values["SPLIT_PANE_ID"] != "pane-42" || values["SPLIT_PROVIDER"] != "codex" {
		t.Fatalf("per-command overrides were not applied: %#v", values)
	}
}
