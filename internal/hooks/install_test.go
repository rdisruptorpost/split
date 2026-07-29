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

	statePath := filepath.Join(directory, "state.db")
	results, err := InstallAll(paths, `C:\Program Files\split\split.exe`, statePath)
	if err != nil {
		t.Fatal(err)
	}
	managedScript, err := os.ReadFile(filepath.Join(directory, "session-hook.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(managedScript), "SPLIT_HOOK_EXE") ||
		!strings.Contains(string(managedScript), "UTF8Encoding]::new($false)") ||
		!strings.Contains(string(managedScript), "Write-SplitHookTrace") ||
		!strings.Contains(string(managedScript), "runtime.log") ||
		!strings.Contains(string(managedScript), "helper_exit_code") ||
		strings.Contains(string(managedScript), `C:\Program Files\split\split.exe`) {
		t.Fatalf("managed hook should use the pane executable and structured diagnostics: %s", managedScript)
	}
	managedStatusLine, err := os.ReadFile(filepath.Join(directory, "claude-statusline.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(managedStatusLine), "provider-usage claude") ||
		!strings.Contains(string(managedStatusLine), "SPLIT_STATE_PATH") {
		t.Fatalf("managed Claude status line does not forward usage safely: %s", managedStatusLine)
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
			!strings.Contains(string(content), "session-hook.ps1") ||
			!strings.Contains(string(content), "session-start "+result.Provider) {
			t.Fatalf("hook merge lost existing or split command: %s", content)
		}
		if result.Provider == "codex" &&
			(!strings.Contains(string(content), "commandWindows") || !strings.Contains(string(content), "powershell.exe")) {
			t.Fatalf("Codex hook is missing its PowerShell Windows override: %s", content)
		}
		if result.Provider == "claude" &&
			(!strings.Contains(string(content), `"statusLine"`) ||
				!strings.Contains(string(content), "claude-statusline.ps1")) {
			t.Fatalf("Claude usage status line was not installed: %s", content)
		}
		var parsed map[string]any
		if err := json.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("installed file is not valid JSON: %v", err)
		}
	}

	results, err = InstallAll(paths, `C:\Program Files\split\split.exe`, statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Changed {
			t.Fatalf("second install should be idempotent: %#v", result)
		}
	}
}

func TestUninstallAllRemovesOnlySplitHooks(t *testing.T) {
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
	if _, err := InstallAll(paths, `C:\Program Files\split\split.exe`, filepath.Join(directory, "state.db")); err != nil {
		t.Fatal(err)
	}

	results, err := UninstallAll(paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if !result.Changed || result.BackupPath == "" {
			t.Fatalf("expected uninstall with backup: %#v", result)
		}
		content, err := os.ReadFile(result.Path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "session-hook.ps1") ||
			strings.Contains(string(content), "claude-statusline.ps1") ||
			strings.Contains(string(content), " hook session-start "+result.Provider) {
			t.Fatalf("split hook remains after uninstall: %s", content)
		}
		if !strings.Contains(string(content), "existing-tool session") ||
			!strings.Contains(string(content), `"permissions"`) {
			t.Fatalf("uninstall removed unrelated configuration: %s", content)
		}
	}

	results, err = UninstallAll(paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Changed {
			t.Fatalf("second uninstall should be idempotent: %#v", result)
		}
	}
}

func TestInstallAllUpgradesLegacyExecutableHooksInPlace(t *testing.T) {
	directory := t.TempDir()
	paths := Paths{
		Codex:  filepath.Join(directory, "codex", "hooks.json"),
		Claude: filepath.Join(directory, "claude", "settings.json"),
	}
	for provider, path := range map[string]string{"codex": paths.Codex, "claude": paths.Claude} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		legacy := `{"hooks":{"SessionStart":[{"matcher":"startup|resume|clear|compact","hooks":[{"type":"command","command":"C:\\old\\split.exe hook session-start ` + provider + ` C:\\old\\state.db"}]}]}}`
		if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := InstallAll(paths, `C:\new\split.exe`, filepath.Join(directory, "state.db")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Codex, paths.Claude} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), `C:\old\split.exe`) ||
			strings.Count(string(content), `"matcher"`) != 1 ||
			!strings.Contains(string(content), "session-hook.ps1") {
			t.Fatalf("legacy hook was not upgraded in place: %s", content)
		}
	}
}

func TestInstallAndUninstallPreserveCustomClaudeStatusLine(t *testing.T) {
	directory := t.TempDir()
	paths := Paths{
		Codex:  filepath.Join(directory, "codex", "hooks.json"),
		Claude: filepath.Join(directory, "claude", "settings.json"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Claude), 0o700); err != nil {
		t.Fatal(err)
	}
	custom := `{"theme":"dark","statusLine":{"type":"command","command":"my-status-tool --compact","padding":2}}`
	if err := os.WriteFile(paths.Claude, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAll(
		paths,
		`C:\Program Files\split\split.exe`,
		filepath.Join(directory, "state.db"),
	); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(paths.Claude)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "my-status-tool --compact") ||
		strings.Contains(string(content), "claude-statusline.ps1") {
		t.Fatalf("custom Claude status line was replaced: %s", content)
	}
	if _, err := UninstallAll(paths); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(paths.Claude)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "my-status-tool --compact") {
		t.Fatalf("custom Claude status line was removed: %s", content)
	}
}
