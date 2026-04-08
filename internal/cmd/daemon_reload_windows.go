//go:build windows

package cmd

import (
	"fmt"
	"os"
)

// signalDaemonReload is unsupported on Windows (no SIGUSR2).
func signalDaemonReload(_ *os.Process) error {
	return fmt.Errorf("daemon reload is not supported on Windows")
}
