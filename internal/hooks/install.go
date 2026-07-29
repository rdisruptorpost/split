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
		return nil, errors.New("split executable path is empty")
	}
	if statePath == "" {
		return nil, errors.New("split state path is empty")
	}
	scriptPath := filepath.Join(filepath.Dir(statePath), "session-hook.ps1")
	if err := writeSessionHookScript(scriptPath); err != nil {
		return nil, err
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
		result, err := installOne(provider.path, provider.name, scriptPath)
		if err != nil {
			return results, fmt.Errorf("install %s hook: %w", provider.name, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func installOne(path, provider, scriptPath string) (Result, error) {
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

	command := `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File ` +
		quoteExecutable(scriptPath) + " session-start " + provider
	commandWindows := ""
	if provider == "codex" {
		commandWindows = command
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
			if !isSplitSessionStartCommand(existing, provider) &&
				!isSplitSessionStartCommand(existingWindows, provider) {
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
	return `"` + path + `"`
}

func isSplitSessionStartCommand(command, provider string) bool {
	command = strings.ToLower(command)
	provider = strings.ToLower(provider)
	legacyMarker := " hook session-start " + provider
	managedMarker := " session-start " + provider
	return strings.Contains(command, legacyMarker) ||
		(strings.Contains(command, "session-hook.ps1") && strings.Contains(command, managedMarker))
}

const sessionHookScript = `param(
    [string]$Action,
    [string]$Provider
)

$ErrorActionPreference = 'SilentlyContinue'

function Write-SplitHookTrace {
    param(
        [string]$EventName,
        [hashtable]$Fields,
        [string]$ErrorText = ''
    )
    try {
        if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
            return
        }
        $splitLogDirectory = Join-Path $env:LOCALAPPDATA 'split'
        [System.IO.Directory]::CreateDirectory($splitLogDirectory) | Out-Null
        $splitEntry = [ordered]@{
            time = [DateTimeOffset]::Now.ToString('o')
            pid = $PID
            component = 'hook-wrapper'
            event = $EventName
        }
        if ($null -ne $Fields -and $Fields.Count -gt 0) {
            $splitEntry.fields = $Fields
        }
        if (-not [string]::IsNullOrWhiteSpace($ErrorText)) {
            $splitEntry.error = $ErrorText
        }
        $splitLine = $splitEntry | ConvertTo-Json -Compress -Depth 4
        $splitLogPath = Join-Path $splitLogDirectory 'runtime.log'
        [System.IO.File]::AppendAllText(
            $splitLogPath,
            $splitLine + [Environment]::NewLine,
            [System.Text.UTF8Encoding]::new($false)
        )
    } catch {
    }
}

$splitTraceEnabled = (
    $env:SPLIT_ENV -eq '1' -or
    $env:TERM_PROGRAM -eq 'split' -or
    -not [string]::IsNullOrWhiteSpace($env:SPLIT_PANE_ID) -or
    -not [string]::IsNullOrWhiteSpace($env:SPLIT_STATE_PATH) -or
    -not [string]::IsNullOrWhiteSpace($env:SPLIT_HOOK_EXE)
)
$splitFields = @{
    action = $Action
    provider = $Provider
    split_env = $env:SPLIT_ENV
    term_program = $env:TERM_PROGRAM
    pane_id = $env:SPLIT_PANE_ID
    state_path = $env:SPLIT_STATE_PATH
    hook_executable = $env:SPLIT_HOOK_EXE
    process_cwd = (Get-Location).Path
}
if ($splitTraceEnabled) {
    Write-SplitHookTrace 'invoked' $splitFields
}

if ($Action -ne 'session-start' -or
    $Provider -notin @('codex', 'claude') -or
    $env:SPLIT_ENV -ne '1' -or
    [string]::IsNullOrWhiteSpace($env:SPLIT_PANE_ID) -or
    [string]::IsNullOrWhiteSpace($env:SPLIT_STATE_PATH) -or
    [string]::IsNullOrWhiteSpace($env:SPLIT_HOOK_EXE)) {
    if ($splitTraceEnabled) {
        Write-SplitHookTrace 'rejected' $splitFields 'split pane environment is incomplete'
    }
    exit 0
}

$splitPreviousOutputEncoding = $OutputEncoding
try {
    $splitPayload = [Console]::In.ReadToEnd()
    $splitFields.payload_chars = $splitPayload.Length.ToString()
    Write-SplitHookTrace 'helper_starting' $splitFields
    $OutputEncoding = [System.Text.UTF8Encoding]::new($false)
    $splitPayload | & $env:SPLIT_HOOK_EXE hook session-start $Provider $env:SPLIT_STATE_PATH 2>$null | Out-Null
    $splitFields.helper_exit_code = $LASTEXITCODE.ToString()
    Write-SplitHookTrace 'helper_finished' $splitFields
} catch {
    Write-SplitHookTrace 'helper_failed' $splitFields $_.Exception.Message
} finally {
    $OutputEncoding = $splitPreviousOutputEncoding
}
exit 0
`

func writeSessionHookScript(path string) error {
	content := []byte(sessionHookScript)
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(content) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read managed session hook: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create managed session hook directory: %w", err)
	}
	if err := replaceFile(path, content); err != nil {
		return fmt.Errorf("write managed session hook: %w", err)
	}
	return nil
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
