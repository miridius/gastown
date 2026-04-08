//go:build windows

package session

// ActivateAgentLogging is a no-op on Windows (agent logging requires Unix process management).
func ActivateAgentLogging(_, _, _ string) error {
	return nil
}

// DeactivateAgentLogging is a no-op on Windows.
func DeactivateAgentLogging(_ string) {}
