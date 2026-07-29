//go:build windows

package usage

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureUsageCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
