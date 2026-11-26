//go:build !windows

package coremgr

import (
	"os"
	"syscall"
)

const supportsSIGHUP = true

func sendReloadSignal(proc *os.Process) error {
	if proc == nil {
		return errNoActiveProcess
	}
	return proc.Signal(syscall.SIGHUP)
}

func terminateProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Signal(syscall.SIGTERM)
}
