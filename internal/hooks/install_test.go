package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAllPreservesExistingHooksAndIsIdempotent(t *testing.T) {
	directory := t.TempDir()
	paths := Paths{
		Codex:  filepath.Join(directory, "codex", "hooks.json"),
		Claude: filepath.Join(directory, "claude", "settings.json"),
	}
	existing := []byte(`{
  "permissions": {"defaultMode": "auto"},
  "hooks": {
    "SessionStart": [{
      "hooks": [{"type": "command", "command": "existing-tool session"}]
    }]
  }
}`)
	for _, path := range []string{paths.Codex, paths.Claude} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, existing, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	results, err := InstallAll(paths, `C:\Program Files\Split\split.exe`, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if !result.Changed || result.BackupPath == "" {
			t.Fatalf("expected changed file with backup: %#v", result)
		}
		content, err := os.ReadFile(result.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "existing-tool session") ||
			!strings.Contains(string(content), "hook session-start "+result.Provider) {
			t.Fatalf("hook merge lost existing or Split command: %s", content)
		}
		if result.Provider == "codex" &&
			(!strings.Contains(string(content), "commandWindows") || !strings.Contains(string(content), "powershell.exe")) {
			t.Fatalf("Codex hook is missing its PowerShell Windows override: %s", content)
		}
		var parsed map[string]any
		if err := json.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("installed file is not valid JSON: %v", err)
		}
	}

	results, err = InstallAll(paths, `C:\Program Files\Split\split.exe`, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Changed {
			t.Fatalf("second install should be idempotent: %#v", result)
		}
	}
}
