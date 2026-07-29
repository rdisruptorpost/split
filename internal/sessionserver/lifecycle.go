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

	"split/internal/diagnostics"
	"split/internal/state"
)

var ErrNotRunning = errors.New("the split runtime is not running")

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
	_ = diagnostics.Append(
		statePath,
		"client",
		"connect_requested",
		diagnostics.Fields{"launch_root": root},
		nil,
	)
	endpoint, err := Endpoint(statePath)
	if err != nil {
		_ = diagnostics.Append(statePath, "client", "endpoint_failed", nil, err)
		return nil, err
	}
	if connection, err := dialWithTimeout(endpoint, 200*time.Millisecond); err == nil {
		_ = diagnostics.Append(statePath, "client", "connected_existing_runtime", nil, nil)
		return newClient(connection)
	}
	if err := state.MigrateLegacyDefaultDirectory(statePath); err != nil {
		_ = diagnostics.Append(statePath, "client", "state_migration_failed", nil, err)
		return nil, err
	}

	executable, err := os.Executable()
	if err != nil {
		err = fmt.Errorf("locate split executable: %w", err)
		_ = diagnostics.Append(statePath, "client", "executable_failed", nil, err)
		return nil, err
	}
	stateDirectory := filepath.Dir(statePath)
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create split state directory: %w", err)
	}
	logPath := filepath.Join(stateDirectory, "runtime.log")
	launchFields := diagnostics.Fields{
		"executable":  executable,
		"launch_root": root,
		"log_path":    logPath,
	}
	_ = diagnostics.Append(statePath, "client", "runtime_launching", launchFields, nil)
	if err := startDetachedRuntime(executable, root, statePath, logPath); err != nil {
		err = fmt.Errorf("start split runtime: %w", err)
		_ = diagnostics.Append(statePath, "client", "runtime_launch_failed", launchFields, err)
		return nil, err
	}

	deadline := time.Now().Add(8 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		connection, err := dialWithTimeout(endpoint, 300*time.Millisecond)
		if err == nil {
			_ = diagnostics.Append(statePath, "client", "connected_started_runtime", launchFields, nil)
			return newClient(connection)
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	err = fmt.Errorf("split runtime did not become ready: %w (see %s)", lastErr, logPath)
	_ = diagnostics.Append(statePath, "client", "runtime_ready_timeout", launchFields, err)
	return nil, err
}

// Stop asks the runtime to persist its state, terminate every child process,
// and exit. Closing a regular UI client never calls Stop.
func Stop(statePath string) error {
	_ = diagnostics.Append(statePath, "client", "stop_connecting", nil, nil)
	endpoint, err := Endpoint(statePath)
	if err != nil {
		_ = diagnostics.Append(statePath, "client", "stop_endpoint_failed", nil, err)
		return err
	}
	connection, err := dialWithTimeout(endpoint, time.Second)
	if err != nil {
		err = fmt.Errorf("%w: %v", ErrNotRunning, err)
		_ = diagnostics.Append(statePath, "client", "stop_connect_failed", nil, err)
		return err
	}
	defer connection.Close()

	if err := json.NewEncoder(connection).Encode(request{Version: protocolVersion, Kind: requestStop}); err != nil {
		return fmt.Errorf("request split runtime stop: %w", err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	var response frame
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return fmt.Errorf("wait for split runtime stop: %w", err)
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	if !response.Stopped {
		err := errors.New("split runtime did not acknowledge the stop request")
		_ = diagnostics.Append(statePath, "client", "stop_not_acknowledged", nil, err)
		return err
	}
	_ = diagnostics.Append(statePath, "client", "stop_completed", nil, nil)
	return nil
}

func dialWithTimeout(endpoint string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return dialEndpoint(ctx, endpoint)
}
