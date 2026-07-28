package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	Codex  string
	Claude string
}

type Result struct {
	Provider   string
	Path       string
	Changed    bool
	BackupPath string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locate home directory: %w", err)
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	return Paths{
		Codex:  filepath.Join(codexHome, "hooks.json"),
		Claude: filepath.Join(claudeHome, "settings.json"),
	}, nil
}

func InstallAll(paths Paths, executable, statePath string) ([]Result, error) {
	if executable == "" {
		return nil, errors.New("Split executable path is empty")
	}
	if statePath == "" {
		return nil, errors.New("Split state path is empty")
	}
	providers := []struct {
		name string
		path string
	}{
		{name: "codex", path: paths.Codex},
		{name: "claude", path: paths.Claude},
	}
	results := make([]Result, 0, len(providers))
	for _, provider := range providers {
		result, err := installOne(provider.path, provider.name, executable, statePath)
		if err != nil {
			return results, fmt.Errorf("install %s hook: %w", provider.name, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func installOne(path, provider, executable, statePath string) (Result, error) {
	result := Result{Provider: provider, Path: path}
	document := make(map[string]any)
	original, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(original, &document); err != nil {
			return result, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		original = nil
	case err != nil:
		return result, fmt.Errorf("read %s: %w", path, err)
	}
	if document == nil {
		document = make(map[string]any)
	}

	command := quoteExecutable(executable) + " hook session-start " + provider + " " + quoteExecutable(statePath)
	commandWindows := ""
	if provider == "codex" {
		commandWindows = powershellHookCommand(executable, provider, statePath)
	}
	changed, err := mergeSessionStartHook(document, provider, command, commandWindows)
	if err != nil {
		return result, err
	}
	if !changed {
		return result, nil
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return result, fmt.Errorf("encode hook configuration: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return result, fmt.Errorf("create hook directory: %w", err)
	}
	if len(original) > 0 {
		backupPath := path + ".split-backup"
		if err := writeBackupOnce(backupPath, original); err != nil {
			return result, err
		}
		result.BackupPath = backupPath
	}
	if err := replaceFile(path, encoded); err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}

func mergeSessionStartHook(document map[string]any, provider, command, commandWindows string) (bool, error) {
	hooksValue, exists := document["hooks"]
	if !exists {
		hooksValue = make(map[string]any)
		document["hooks"] = hooksValue
	}
	hooksMap, ok := hooksValue.(map[string]any)
	if !ok {
		return false, errors.New("hooks configuration is not an object")
	}

	sessionValue, exists := hooksMap["SessionStart"]
	if !exists {
		sessionValue = []any{}
	}
	sessionHooks, ok := sessionValue.([]any)
	if !ok {
		return false, errors.New("hooks.SessionStart configuration is not an array")
	}
	marker := " hook session-start " + provider
	for _, groupValue := range sessionHooks {
		group, ok := groupValue.(map[string]any)
		if !ok {
			continue
		}
		handlers, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, handlerValue := range handlers {
			handler, ok := handlerValue.(map[string]any)
			if !ok {
				continue
			}
			existing, _ := handler["command"].(string)
			existingWindows, _ := handler["commandWindows"].(string)
			if !strings.Contains(existing, marker) && !strings.Contains(existingWindows, marker) {
				continue
			}
			changed := false
			if existing != command {
				handler["command"] = command
				changed = true
			}
			if commandWindows != "" && existingWindows != commandWindows {
				handler["commandWindows"] = commandWindows
				changed = true
			}
			return changed, nil
		}
	}

	handler := map[string]any{
		"type":    "command",
		"command": command,
		"timeout": 5,
	}
	if commandWindows != "" {
		handler["commandWindows"] = commandWindows
	}
	group := map[string]any{
		"matcher": "startup|resume|clear|compact",
		"hooks":   []any{handler},
	}
	hooksMap["SessionStart"] = append(sessionHooks, group)
	return true, nil
}

func quoteExecutable(path string) string {
	return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"`
}

func powershellHookCommand(executable, provider, statePath string) string {
	executable = strings.ReplaceAll(executable, "'", "''")
	statePath = strings.ReplaceAll(statePath, "'", "''")
	return `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "& '` +
		executable + `' hook session-start ` + provider + ` '` + statePath + `'"`
}

func writeBackupOnce(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create hook backup: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		return fmt.Errorf("write hook backup: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync hook backup: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close hook backup: %w", err)
	}
	return nil
}

func replaceFile(path string, content []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".split-hooks-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary hook file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary hook permissions: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary hook file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary hook file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary hook file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("replace hook configuration: %w", err)
	}
	return nil
}
