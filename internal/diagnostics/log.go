package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const logFileName = "runtime.log"

// Fields contains compact diagnostic metadata. Terminal output, prompts, and
// chat content must never be placed here.
type Fields map[string]string

type entry struct {
	Time      string `json:"time"`
	PID       int    `json:"pid"`
	Component string `json:"component"`
	Event     string `json:"event"`
	Fields    Fields `json:"fields,omitempty"`
	Error     string `json:"error,omitempty"`
}

var appendMu sync.Mutex

// LogPath returns the runtime diagnostic log beside the SQLite database.
func LogPath(statePath string) string {
	if strings.TrimSpace(statePath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Clean(statePath)), logFileName)
}

// Append writes one JSON object per line. It opens the file for each event so
// the UI, detached runtime, and short-lived hook helper can all contribute
// without keeping a second long-lived file handle.
func Append(
	statePath, component, eventName string,
	fields Fields,
	eventErr error,
) error {
	path := LogPath(statePath)
	if path == "" {
		return errors.New("diagnostic state path is empty")
	}
	component = strings.TrimSpace(component)
	eventName = strings.TrimSpace(eventName)
	if component == "" || eventName == "" {
		return errors.New("diagnostic component and event are required")
	}

	value := entry{
		Time:      time.Now().Format(time.RFC3339Nano),
		PID:       os.Getpid(),
		Component: component,
		Event:     eventName,
		Fields:    cloneFields(fields),
	}
	if eventErr != nil {
		value.Error = eventErr.Error()
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode diagnostic event: %w", err)
	}
	encoded = append(encoded, '\n')

	appendMu.Lock()
	defer appendMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create diagnostic directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open diagnostic log: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return fmt.Errorf("append diagnostic event: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close diagnostic log: %w", err)
	}
	return nil
}

func cloneFields(fields Fields) Fields {
	if len(fields) == 0 {
		return nil
	}
	cloned := make(Fields, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}
