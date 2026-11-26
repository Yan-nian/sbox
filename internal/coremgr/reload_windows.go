//go:build windows

package coremgr

import (
	"os"
)

const supportsSIGHUP = false

func sendReloadSignal(_ *os.Process) error {
	return errReloadUnsupported
}

func terminateProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Kill()
}
