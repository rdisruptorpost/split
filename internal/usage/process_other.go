//go:build !windows

package usage

import "os/exec"

func configureUsageCommand(_ *exec.Cmd) {}
