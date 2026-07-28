package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type sessionEvent struct {
	PaneID           string `json:"pane_id"`
	Provider         string `json:"provider"`
	SessionID        string `json:"session_id"`
	WorkingDirectory string `json:"working_directory"`
	CreatedAt        int64  `json:"created_at"`
}

type sessionLaunch struct {
	PaneID           string `json:"pane_id"`
	Provider         string `json:"provider"`
	WorkingDirectory string `json:"working_directory"`
	CreatedAt        int64  `json:"created_at"`
}

func RegisterSessionLaunch(databasePath, paneID, provider, workingDirectory string) error {
	if databasePath == "" || paneID == "" || provider == "" {
		return errors.New("database path, pane id, and provider are required")
	}
	claim := sessionLaunch{
		PaneID:           paneID,
		Provider:         provider,
		WorkingDirectory: workingDirectory,
		CreatedAt:        time.Now().UnixMilli(),
	}
	encoded, err := json.Marshal(claim)
	if err != nil {
		return fmt.Errorf("encode session launch: %w", err)
	}
	directory := sessionLaunchesDirectory(databasePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create session launch directory: %w", err)
	}
	file, err := os.CreateTemp(directory, fmt.Sprintf("%020d-*.json", time.Now().UnixNano()))
	if err != nil {
		return fmt.Errorf("create session launch: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("secure session launch: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write session launch: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("sync session launch: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close session launch: %w", err)
	}
	return nil
}

func HasPendingSessionLaunch(databasePath, provider string) bool {
	claims := readSessionLaunches(databasePath)
	for _, candidate := range claims {
		if candidate.claim.Provider == provider {
			return true
		}
	}
	return false
}

func ClaimSessionEvent(databasePath, provider, sessionID, workingDirectory, transcriptPath, source string) (bool, error) {
	claims := readSessionLaunches(databasePath)
	targetTime := sessionStartTimeMillis(sessionID, transcriptPath, source)
	bestIndex := -1
	bestDistance := int64(^uint64(0) >> 1)
	for index, candidate := range claims {
		if candidate.claim.Provider != provider || !sameWorkingDirectory(candidate.claim.WorkingDirectory, workingDirectory) {
			continue
		}
		distance := candidate.claim.CreatedAt - targetTime
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			bestIndex = index
			bestDistance = distance
		}
	}
	if bestIndex < 0 {
		return false, nil
	}
	candidate := claims[bestIndex]
	if err := QueueSessionEvent(databasePath, candidate.claim.PaneID, provider, sessionID, workingDirectory); err != nil {
		return false, err
	}
	if err := os.Remove(candidate.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, fmt.Errorf("consume session launch: %w", err)
	}
	return true, nil
}

type sessionLaunchCandidate struct {
	path  string
	claim sessionLaunch
}

func readSessionLaunches(databasePath string) []sessionLaunchCandidate {
	directory := sessionLaunchesDirectory(databasePath)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-24 * time.Hour).UnixMilli()
	claims := make([]sessionLaunchCandidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		encoded, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var claim sessionLaunch
		if err := json.Unmarshal(encoded, &claim); err != nil {
			_ = os.Rename(path, path+".bad")
			continue
		}
		if claim.CreatedAt < cutoff {
			_ = os.Remove(path)
			continue
		}
		claims = append(claims, sessionLaunchCandidate{path: path, claim: claim})
	}
	return claims
}

func sessionStartTimeMillis(sessionID, transcriptPath, source string) int64 {
	compactID := strings.ReplaceAll(sessionID, "-", "")
	if source == "startup" && len(compactID) == 32 && compactID[12] == '7' {
		if value, err := strconv.ParseInt(compactID[:12], 16, 64); err == nil {
			return value
		}
	}
	if transcriptPath != "" {
		if info, err := os.Stat(transcriptPath); err == nil {
			return info.ModTime().UnixMilli()
		}
	}
	return time.Now().UnixMilli()
}

func sameWorkingDirectory(first, second string) bool {
	return strings.EqualFold(filepath.Clean(first), filepath.Clean(second))
}

func QueueSessionEvent(databasePath, paneID, provider, sessionID, workingDirectory string) error {
	if databasePath == "" {
		return errors.New("state database path is empty")
	}
	if paneID == "" || provider == "" || sessionID == "" {
		return errors.New("pane id, provider, and session id are required")
	}
	event := sessionEvent{
		PaneID:           paneID,
		Provider:         provider,
		SessionID:        sessionID,
		WorkingDirectory: workingDirectory,
		CreatedAt:        time.Now().UnixMilli(),
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode session event: %w", err)
	}

	directory := sessionEventsDirectory(databasePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create session event directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, fmt.Sprintf("%020d-*.tmp", time.Now().UnixNano()))
	if err != nil {
		return fmt.Errorf("create session event: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure session event: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return fmt.Errorf("write session event: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync session event: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close session event: %w", err)
	}
	finalPath := strings.TrimSuffix(temporaryPath, ".tmp") + ".json"
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return fmt.Errorf("publish session event: %w", err)
	}
	return nil
}

func (s *Store) importSessionEvents() {
	directory := sessionEventsDirectory(s.path)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		_ = AppendHookDiagnostic(s.path, "state", "", fmt.Errorf("read pending session events: %w", err))
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		encoded, err := os.ReadFile(path)
		if err != nil {
			_ = AppendHookDiagnostic(s.path, "state", "", fmt.Errorf("read session event %s: %w", entry.Name(), err))
			continue
		}
		var event sessionEvent
		if err := json.Unmarshal(encoded, &event); err != nil {
			_ = AppendHookDiagnostic(s.path, "state", "", fmt.Errorf("decode session event %s: %w", entry.Name(), err))
			_ = os.Rename(path, path+".bad")
			continue
		}
		if err := s.BindSession(event.PaneID, event.Provider, event.SessionID, event.WorkingDirectory); err != nil {
			_ = AppendHookDiagnostic(s.path, event.Provider, event.PaneID, fmt.Errorf("import session event: %w", err))
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = AppendHookDiagnostic(s.path, event.Provider, event.PaneID, fmt.Errorf("remove imported session event: %w", err))
		}
	}
}

func AppendHookDiagnostic(databasePath, provider, paneID string, hookErr error) error {
	if hookErr == nil {
		return nil
	}
	directory := filepath.Dir(databasePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, "hook.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "%s provider=%s pane=%s error=%s\n",
		time.Now().Format(time.RFC3339Nano), provider, paneID, strings.ReplaceAll(hookErr.Error(), "\n", " "))
	return err
}

func sessionEventsDirectory(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "session-events")
}

func sessionLaunchesDirectory(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "session-launches")
}
