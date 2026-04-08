//go:build windows

package daemon

import "os"

func daemonSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func isLifecycleSignal(_ os.Signal) bool {
	return false
}

func isReloadRestartSignal(_ os.Signal) bool {
	return false
}
