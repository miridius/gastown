package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestControlleManager_IsEnabled(t *testing.T) {
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
			m := NewControlleManager("/tmp/test-town", tt.config, t.Logf)
			if got := m.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestControlleManager_Defaults(t *testing.T) {
	m := NewControlleManager("/tmp/test-town", &ControlleConfig{Enabled: true}, t.Logf)

	wantWorkDir := filepath.Join("/tmp/test-town", "..", "controlle", "crew", "sam")
	if m.config.WorkDir != wantWorkDir {
		t.Errorf("WorkDir = %q, want %q", m.config.WorkDir, wantWorkDir)
	}

	wantRuntime := filepath.Join(wantWorkDir, ".runtime")
	if m.config.RuntimeDir != wantRuntime {
		t.Errorf("RuntimeDir = %q, want %q", m.config.RuntimeDir, wantRuntime)
	}
}

func TestControlleManager_EnsureRunning_Disabled(t *testing.T) {
	m := NewControlleManager("/tmp/test-town", &ControlleConfig{Enabled: false}, t.Logf)
	if err := m.EnsureRunning(); err != nil {
		t.Errorf("EnsureRunning() on disabled manager returned error: %v", err)
	}
}

func TestControlleManager_IsRunning_NoPidFile(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewControlleManager("/tmp/test-town", &ControlleConfig{
		Enabled:    true,
		WorkDir:    tmpDir,
		RuntimeDir: tmpDir,
	}, t.Logf)

	m.mu.Lock()
	_, running := m.isRunning()
	m.mu.Unlock()

	if running {
		t.Error("isRunning() = true with no PID file, want false")
	}
}

func TestControlleManager_IsRunning_StalePidFile(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewControlleManager("/tmp/test-town", &ControlleConfig{
		Enabled:    true,
		WorkDir:    tmpDir,
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

func TestControlleManager_Start_MissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewControlleManager("/tmp/test-town", &ControlleConfig{
		Enabled:    true,
		WorkDir:    tmpDir,
		RuntimeDir: tmpDir,
	}, t.Logf)

	m.mu.Lock()
	err := m.start()
	m.mu.Unlock()

	if err == nil {
		t.Error("start() with missing source should return error")
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
