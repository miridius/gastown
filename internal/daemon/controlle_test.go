package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestControlleServerManager_IsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config *ControlleConfig
		want   bool
	}{
		{"nil config", nil, false},
		{"disabled", &ControlleConfig{Enabled: false}, false},
		{"enabled", &ControlleConfig{Enabled: true}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewControlleServerManager("/tmp/test-town", tt.config, t.Logf)
			if got := m.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestControlleServerManager_Defaults(t *testing.T) {
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{Enabled: true}, t.Logf)

	wantDeployDir := filepath.Join("/tmp/test-town", "..", "controlle", "refinery", "rig")
	if m.config.DeployDir != wantDeployDir {
		t.Errorf("DeployDir = %q, want %q", m.config.DeployDir, wantDeployDir)
	}

	wantRuntime := filepath.Join("/tmp/test-town", "..", "controlle", ".runtime")
	if m.config.RuntimeDir != wantRuntime {
		t.Errorf("RuntimeDir = %q, want %q", m.config.RuntimeDir, wantRuntime)
	}

	if m.config.RestartDelay != 5*time.Second {
		t.Errorf("RestartDelay = %v, want 5s", m.config.RestartDelay)
	}
	if m.config.MaxRestartDelay != 5*time.Minute {
		t.Errorf("MaxRestartDelay = %v, want 5m", m.config.MaxRestartDelay)
	}
	if m.config.MaxRestartsInWindow != 5 {
		t.Errorf("MaxRestartsInWindow = %d, want 5", m.config.MaxRestartsInWindow)
	}
	if m.config.RestartWindow != 10*time.Minute {
		t.Errorf("RestartWindow = %v, want 10m", m.config.RestartWindow)
	}
	if m.config.HealthCheckInterval != DefaultControlleHealthCheckInterval {
		t.Errorf("HealthCheckInterval = %v, want %v", m.config.HealthCheckInterval, DefaultControlleHealthCheckInterval)
	}
}

func TestControlleServerManager_WorkDirMigration(t *testing.T) {
	// Test that deprecated WorkDir is migrated to DeployDir
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled: true,
		WorkDir: "/custom/workdir",
	}, t.Logf)

	if m.config.DeployDir != "/custom/workdir" {
		t.Errorf("DeployDir = %q after WorkDir migration, want %q", m.config.DeployDir, "/custom/workdir")
	}
}

func TestControlleServerManager_DeployDirOverridesWorkDir(t *testing.T) {
	// DeployDir takes precedence over WorkDir
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled:   true,
		DeployDir: "/deploy/dir",
		WorkDir:   "/work/dir",
	}, t.Logf)

	if m.config.DeployDir != "/deploy/dir" {
		t.Errorf("DeployDir = %q, want %q (should not be overridden by WorkDir)", m.config.DeployDir, "/deploy/dir")
	}
}

func TestControlleServerManager_EnsureRunning_Disabled(t *testing.T) {
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{Enabled: false}, t.Logf)
	if err := m.EnsureRunning(); err != nil {
		t.Errorf("EnsureRunning() on disabled manager returned error: %v", err)
	}
}

func TestControlleServerManager_IsRunning_NoPidFile(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled:    true,
		DeployDir:  tmpDir,
		RuntimeDir: tmpDir,
	}, t.Logf)

	m.mu.Lock()
	_, running := m.isRunning()
	m.mu.Unlock()

	if running {
		t.Error("isRunning() = true with no PID file, want false")
	}
}

func TestControlleServerManager_IsRunning_StalePidFile(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled:    true,
		DeployDir:  tmpDir,
		RuntimeDir: tmpDir,
	}, t.Logf)

	// Write a PID file with a non-existent PID
	pidFile := filepath.Join(tmpDir, "controlle.pid")
	os.WriteFile(pidFile, []byte("999999\nfakenonce"), 0644)

	m.mu.Lock()
	_, running := m.isRunning()
	m.mu.Unlock()

	if running {
		t.Error("isRunning() = true with stale PID, want false")
	}

	// Stale PID file should be cleaned up
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("stale PID file was not cleaned up")
	}
}

func TestControlleServerManager_Start_MissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled:    true,
		DeployDir:  tmpDir,
		RuntimeDir: tmpDir,
	}, t.Logf)

	err := m.Start()
	if err == nil {
		t.Error("Start() with missing source should return error")
	}
}

func TestControlleServerManager_HealthCheckInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{"default", 0, DefaultControlleHealthCheckInterval},
		{"custom", 60 * time.Second, 60 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
				Enabled:             true,
				HealthCheckInterval: tt.interval,
			}, t.Logf)
			if got := m.HealthCheckInterval(); got != tt.want {
				t.Errorf("HealthCheckInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestControlleServerManager_BackoffLogic(t *testing.T) {
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled:         true,
		AutoRestart:     true,
		RestartDelay:    100 * time.Millisecond,
		MaxRestartDelay: 800 * time.Millisecond,
	}, t.Logf)

	// Initial delay should be RestartDelay
	if got := m.getBackoffDelay(); got != 100*time.Millisecond {
		t.Errorf("initial getBackoffDelay() = %v, want 100ms", got)
	}

	// Advance should double
	m.advanceBackoff()
	if got := m.getBackoffDelay(); got != 200*time.Millisecond {
		t.Errorf("after 1 advance, getBackoffDelay() = %v, want 200ms", got)
	}

	m.advanceBackoff()
	if got := m.getBackoffDelay(); got != 400*time.Millisecond {
		t.Errorf("after 2 advances, getBackoffDelay() = %v, want 400ms", got)
	}

	m.advanceBackoff()
	if got := m.getBackoffDelay(); got != 800*time.Millisecond {
		t.Errorf("after 3 advances, getBackoffDelay() = %v, want 800ms (max)", got)
	}

	// Should be capped at max
	m.advanceBackoff()
	if got := m.getBackoffDelay(); got != 800*time.Millisecond {
		t.Errorf("after 4 advances, getBackoffDelay() = %v, want 800ms (capped)", got)
	}
}

func TestControlleServerManager_PruneRestartTimes(t *testing.T) {
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled:       true,
		RestartWindow: 10 * time.Minute,
	}, t.Logf)

	now := time.Now()
	m.restartTimes = []time.Time{
		now.Add(-15 * time.Minute), // Outside window
		now.Add(-11 * time.Minute), // Outside window
		now.Add(-5 * time.Minute),  // Inside window
		now.Add(-1 * time.Minute),  // Inside window
	}

	m.pruneRestartTimes(now)

	if len(m.restartTimes) != 2 {
		t.Errorf("after pruning, len(restartTimes) = %d, want 2", len(m.restartTimes))
	}
}

func TestControlleServerManager_MaybeResetBackoff(t *testing.T) {
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled:              true,
		HealthyResetInterval: 1 * time.Second,
	}, t.Logf)

	now := time.Now()
	m.nowFn = func() time.Time { return now }

	// Set up some backoff state
	m.currentDelay = 10 * time.Second
	m.restartTimes = []time.Time{now}
	m.escalated = true
	m.lastHealthyTime = now.Add(-2 * time.Second) // 2s ago, > 1s threshold

	m.maybeResetBackoff()

	if m.currentDelay != 0 {
		t.Errorf("after reset, currentDelay = %v, want 0", m.currentDelay)
	}
	if m.restartTimes != nil {
		t.Errorf("after reset, restartTimes should be nil")
	}
	if m.escalated {
		t.Errorf("after reset, escalated should be false")
	}
}

func TestControlleServerManager_RestartCap(t *testing.T) {
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled:             true,
		AutoRestart:         true,
		MaxRestartsInWindow: 2,
		RestartWindow:       10 * time.Minute,
	}, t.Logf)

	now := time.Now()
	m.nowFn = func() time.Time { return now }

	// Inject test hooks to avoid actually starting processes
	m.startFn = func() error { return nil }
	m.runningFn = func() (int, bool) { return 0, false }
	escalated := false
	m.escalateFn = func(count int) { escalated = true }

	// Fill up restart window
	m.restartTimes = []time.Time{
		now.Add(-2 * time.Minute),
		now.Add(-1 * time.Minute),
	}

	err := m.restartWithBackoff()
	if err == nil {
		t.Error("restartWithBackoff() should return error when cap exceeded")
	}
	if !escalated {
		t.Error("escalation should have been triggered")
	}
	if !m.escalated {
		t.Error("m.escalated should be true")
	}
}

func TestControlleServerManager_EnsureRunning_WithTestHooks(t *testing.T) {
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled:     true,
		AutoRestart: true,
	}, t.Logf)

	started := false
	m.startFn = func() error {
		started = true
		return nil
	}
	m.runningFn = func() (int, bool) { return 0, false }
	m.crashAlertFn = func(pid int) {} // suppress crash alerts

	if err := m.EnsureRunning(); err != nil {
		t.Errorf("EnsureRunning() returned error: %v", err)
	}
	if !started {
		t.Error("EnsureRunning() should have called start when not running")
	}
}

func TestControlleServerManager_EnsureRunning_AlreadyRunning(t *testing.T) {
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled: true,
	}, t.Logf)

	started := false
	m.startFn = func() error {
		started = true
		return nil
	}
	m.runningFn = func() (int, bool) { return 12345, true }

	if err := m.EnsureRunning(); err != nil {
		t.Errorf("EnsureRunning() returned error: %v", err)
	}
	if started {
		t.Error("EnsureRunning() should NOT call start when already running")
	}
}

func TestControlleServerManager_Stop_WithTestHook(t *testing.T) {
	m := NewControlleServerManager("/tmp/test-town", &ControlleConfig{
		Enabled: true,
	}, t.Logf)

	stopped := false
	m.stopFn = func() { stopped = true }

	// Set a fake process so Stop enters the branch
	m.process = &os.Process{}

	if err := m.Stop(); err != nil {
		t.Errorf("Stop() returned error: %v", err)
	}
	if !stopped {
		t.Error("Stop() should have called stopFn")
	}
}

func TestIsPatrolEnabled_Controlle(t *testing.T) {
	tests := []struct {
		name   string
		config *DaemonPatrolConfig
		want   bool
	}{
		{"nil config", nil, false},
		{"nil patrols", &DaemonPatrolConfig{}, false},
		{"nil controlle", &DaemonPatrolConfig{Patrols: &PatrolsConfig{}}, false},
		{"disabled", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			Controlle: &ControlleConfig{Enabled: false},
		}}, false},
		{"enabled", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			Controlle: &ControlleConfig{Enabled: true},
		}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPatrolEnabled(tt.config, "controlle"); got != tt.want {
				t.Errorf("IsPatrolEnabled(controlle) = %v, want %v", got, tt.want)
			}
		})
	}
}

// Backward compatibility: NewControlleManager should work as an alias
func TestNewControlleManager_BackwardCompat(t *testing.T) {
	m := NewControlleManager("/tmp/test-town", &ControlleConfig{Enabled: true}, t.Logf)
	if !m.IsEnabled() {
		t.Error("NewControlleManager backward compat: IsEnabled() should be true")
	}
}
