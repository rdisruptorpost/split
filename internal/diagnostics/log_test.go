package diagnostics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAppendWritesConcurrentJSONLines(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	const count = 32

	var workers sync.WaitGroup
	for index := 0; index < count; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := Append(
				statePath,
				"test",
				"event",
				Fields{"index": fmt.Sprint(index)},
				nil,
			); err != nil {
				t.Errorf("append event: %v", err)
			}
		}()
	}
	workers.Wait()

	file, err := os.Open(LogPath(statePath))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	lines := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var value entry
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			t.Fatalf("invalid JSON line %q: %v", scanner.Text(), err)
		}
		if value.Component != "test" || value.Event != "event" ||
			value.Time == "" || value.PID == 0 || value.Fields["index"] == "" {
			t.Fatalf("incomplete diagnostic event: %#v", value)
		}
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != count {
		t.Fatalf("diagnostic line count = %d, want %d", lines, count)
	}
}

func TestAppendRecordsErrors(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	if err := Append(
		statePath,
		"test",
		"failed",
		nil,
		fmt.Errorf("example failure"),
	); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(LogPath(statePath))
	if err != nil {
		t.Fatal(err)
	}
	var value entry
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	if value.Error != "example failure" {
		t.Fatalf("diagnostic error = %q", value.Error)
	}
}
