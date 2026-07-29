//go:build windows

package hooks

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedSessionHookLogsWrapperLifecycle(t *testing.T) {
	directory := t.TempDir()
	scriptPath := filepath.Join(directory, "session-hook.ps1")
	if err := writeSessionHookScript(scriptPath); err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(directory, "hook-helper.cmd")
	if err := os.WriteFile(helperPath, []byte("@echo off\r\nmore >nul\r\nexit /b 0\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	agentDirectory := filepath.Join(directory, "PrismLab workspace")
	if err := os.MkdirAll(agentDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	localAppData := filepath.Join(directory, "local app data")
	statePath := filepath.Join(localAppData, "split", "state.db")

	command := exec.Command(
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-File",
		scriptPath,
		"session-start",
		"codex",
	)
	command.Dir = agentDirectory
	command.Env = overriddenEnvironment(map[string]string{
		"LOCALAPPDATA":     localAppData,
		"SPLIT_ENV":        "1",
		"TERM_PROGRAM":     "split",
		"SPLIT_PANE_ID":    "pane-prismlab",
		"SPLIT_STATE_PATH": statePath,
		"SPLIT_HOOK_EXE":   helperPath,
	})
	command.Stdin = strings.NewReader(
		`{"session_id":"019f-wrapper-test","cwd":"` +
			strings.ReplaceAll(agentDirectory, `\`, `\\`) +
			`","hook_event_name":"SessionStart"}`,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("managed hook failed: %v\n%s", err, output)
	}

	logPath := filepath.Join(localAppData, "split", "runtime.log")
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	seen := make(map[string]map[string]any)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var value struct {
			Event  string         `json:"event"`
			Fields map[string]any `json:"fields"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			t.Fatalf("invalid hook diagnostic line %q: %v", scanner.Text(), err)
		}
		seen[value.Event] = value.Fields
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for _, eventName := range []string{"invoked", "helper_starting", "helper_finished"} {
		if _, exists := seen[eventName]; !exists {
			t.Fatalf("managed hook did not log %q: %#v", eventName, seen)
		}
	}
	finished := seen["helper_finished"]
	if finished["pane_id"] != "pane-prismlab" ||
		finished["helper_exit_code"] != "0" ||
		!strings.EqualFold(finished["process_cwd"].(string), agentDirectory) {
		t.Fatalf("managed hook logged incorrect lifecycle fields: %#v", finished)
	}
}

func overriddenEnvironment(overrides map[string]string) []string {
	environment := os.Environ()
	for key, value := range overrides {
		prefix := strings.ToUpper(key) + "="
		replaced := false
		for index, entry := range environment {
			if strings.HasPrefix(strings.ToUpper(entry), prefix) {
				environment[index] = key + "=" + value
				replaced = true
				break
			}
		}
		if !replaced {
			environment = append(environment, key+"="+value)
		}
	}
	return environment
}
