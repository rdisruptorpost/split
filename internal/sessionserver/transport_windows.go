//go:build windows

package sessionserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func endpointForState(statePath string) (string, error) {
	absolute, err := filepath.Abs(statePath)
	if err != nil {
		return "", err
	}
	normalized := strings.ToLower(filepath.Clean(absolute))
	digest := sha256.Sum256([]byte(normalized))
	return `\\.\pipe\split-` + hex.EncodeToString(digest[:12]), nil
}

func listenEndpoint(endpoint string) (net.Listener, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid := user.User.Sid.String()
	configuration := &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")(A;;GA;;;SY)",
	}
	return winio.ListenPipe(endpoint, configuration)
}

func dialEndpoint(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
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
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
