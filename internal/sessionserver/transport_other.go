//go:build !windows

package sessionserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func endpointForState(statePath string) (string, error) {
	absolute, err := filepath.Abs(statePath)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(filepath.Clean(absolute)))
	return filepath.Join(os.TempDir(), "split-"+hex.EncodeToString(digest[:12])+".sock"), nil
}

func listenEndpoint(endpoint string) (net.Listener, error) {
	return net.Listen("unix", endpoint)
}

func dialEndpoint(ctx context.Context, endpoint string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", endpoint)
}

func startDetachedRuntime(executable, root, statePath, logPath string) error {
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	command := exec.Command(executable, "server", "run", root, statePath)
	command.Dir = root
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
