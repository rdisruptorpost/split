package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// UninstallAll removes only split-owned SessionStart handlers and Claude
// status-line integration while preserving every unrelated provider setting.
func UninstallAll(paths Paths) ([]Result, error) {
	providers := []struct {
		name string
		path string
	}{
		{name: "codex", path: paths.Codex},
		{name: "claude", path: paths.Claude},
	}
	results := make([]Result, 0, len(providers))
	for _, provider := range providers {
		result, err := uninstallOne(provider.path, provider.name)
		if err != nil {
			return results, fmt.Errorf("uninstall %s hook: %w", provider.name, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func uninstallOne(path, provider string) (Result, error) {
	result := Result{Provider: provider, Path: path}
	original, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(original, &document); err != nil {
		return result, fmt.Errorf("parse %s: %w", path, err)
	}
	changed, err := removeSplitSessionStartHooks(document, provider)
	if err != nil {
		return result, err
	}
	if provider == "claude" && removeSplitClaudeStatusLine(document) {
		changed = true
	}
	if !changed {
		return result, nil
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return result, fmt.Errorf("encode hook configuration: %w", err)
	}
	encoded = append(encoded, '\n')
	backupPath := path + ".split-uninstall-backup"
	if err := writeBackupOnce(backupPath, original); err != nil {
		return result, err
	}
	if err := replaceFile(path, encoded); err != nil {
		return result, err
	}
	result.Changed = true
	result.BackupPath = backupPath
	return result, nil
}

func removeSplitClaudeStatusLine(document map[string]any) bool {
	value, exists := document["statusLine"]
	if !exists {
		return false
	}
	statusLine, ok := value.(map[string]any)
	if !ok {
		return false
	}
	command, _ := statusLine["command"].(string)
	if !isSplitClaudeStatusLineCommand(command) {
		return false
	}
	delete(document, "statusLine")
	return true
}

func removeSplitSessionStartHooks(document map[string]any, provider string) (bool, error) {
	hooksValue, exists := document["hooks"]
	if !exists {
		return false, nil
	}
	hooksMap, ok := hooksValue.(map[string]any)
	if !ok {
		return false, errors.New("hooks configuration is not an object")
	}
	sessionValue, exists := hooksMap["SessionStart"]
	if !exists {
		return false, nil
	}
	sessionHooks, ok := sessionValue.([]any)
	if !ok {
		return false, errors.New("hooks.SessionStart configuration is not an array")
	}

	changed := false
	groups := make([]any, 0, len(sessionHooks))
	for _, groupValue := range sessionHooks {
		group, ok := groupValue.(map[string]any)
		if !ok {
			groups = append(groups, groupValue)
			continue
		}
		handlersValue, ok := group["hooks"]
		if !ok {
			groups = append(groups, groupValue)
			continue
		}
		handlers, ok := handlersValue.([]any)
		if !ok {
			groups = append(groups, groupValue)
			continue
		}
		kept := make([]any, 0, len(handlers))
		for _, handlerValue := range handlers {
			handler, ok := handlerValue.(map[string]any)
			if !ok {
				kept = append(kept, handlerValue)
				continue
			}
			command, _ := handler["command"].(string)
			commandWindows, _ := handler["commandWindows"].(string)
			if isSplitSessionStartCommand(command, provider) ||
				isSplitSessionStartCommand(commandWindows, provider) {
				changed = true
				continue
			}
			kept = append(kept, handlerValue)
		}
		if len(kept) == 0 && len(handlers) > 0 {
			continue
		}
		if len(kept) != len(handlers) {
			group["hooks"] = kept
		}
		groups = append(groups, group)
	}
	if !changed {
		return false, nil
	}
	if len(groups) == 0 {
		delete(hooksMap, "SessionStart")
	} else {
		hooksMap["SessionStart"] = groups
	}
	if len(hooksMap) == 0 {
		delete(document, "hooks")
	}
	return true, nil
}
