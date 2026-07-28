package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

var ErrNotRunning = errors.New("the Split runtime is not running")

// Endpoint returns the per-state-database local transport address.
func Endpoint(statePath string) (string, error) {
	if statePath == "" {
		return "", errors.New("state database path is empty")
	}
	return endpointForState(statePath)
}

// ConnectOrStart attaches to the existing runtime, or starts it in the
// background and waits for it to become ready.
func ConnectOrStart(root, statePath string) (*Client, error) {
	endpoint, err := Endpoint(statePath)
	if err != nil {
		return nil, err
	}
	if connection, err := dialWithTimeout(endpoint, 200*time.Millisecond); err == nil {
		return newClient(connection)
	}

	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate Split executable: %w", err)
	}
	stateDirectory := filepath.Dir(statePath)
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create Split state directory: %w", err)
	}
	logPath := filepath.Join(stateDirectory, "runtime.log")
	if err := startDetachedRuntime(executable, root, statePath, logPath); err != nil {
		return nil, fmt.Errorf("start Split runtime: %w", err)
	}

	deadline := time.Now().Add(8 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		connection, err := dialWithTimeout(endpoint, 300*time.Millisecond)
		if err == nil {
			return newClient(connection)
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("Split runtime did not become ready: %w (see %s)", lastErr, logPath)
}

// Stop asks the runtime to persist its state, terminate every child process,
// and exit. Closing a regular UI client never calls Stop.
func Stop(statePath string) error {
	endpoint, err := Endpoint(statePath)
	if err != nil {
		return err
	}
	connection, err := dialWithTimeout(endpoint, time.Second)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	defer connection.Close()

	if err := json.NewEncoder(connection).Encode(request{Version: protocolVersion, Kind: requestStop}); err != nil {
		return fmt.Errorf("request Split runtime stop: %w", err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	var response frame
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return fmt.Errorf("wait for Split runtime stop: %w", err)
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	if !response.Stopped {
		return errors.New("Split runtime did not acknowledge the stop request")
	}
	return nil
}

func dialWithTimeout(endpoint string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return dialEndpoint(ctx, endpoint)
}
