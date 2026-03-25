package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultDashboardConfig(t *testing.T) {
	cfg := DefaultDashboardConfig()
	if cfg.Enabled {
		t.Error("expected dashboard to be disabled by default")
	}
	if cfg.Port != DefaultDashboardPort {
		t.Errorf("expected default port %d, got %d", DefaultDashboardPort, cfg.Port)
	}
	if cfg.Bind != "127.0.0.1" {
		t.Errorf("expected default bind 127.0.0.1, got %s", cfg.Bind)
	}
	if !cfg.AutoRestart {
		t.Error("expected auto-restart to be enabled by default")
	}
}

func TestDashboardManager_IsEnabled(t *testing.T) {
	tmpDir := t.TempDir()

	// Disabled by default
	mgr := NewDashboardManager(tmpDir, nil, t.Logf)
	if mgr.IsEnabled() {
		t.Error("expected disabled with nil config")
	}

	// Explicitly disabled
	mgr = NewDashboardManager(tmpDir, &DashboardConfig{Enabled: false}, t.Logf)
	if mgr.IsEnabled() {
		t.Error("expected disabled with Enabled=false")
	}

	// Enabled
	mgr = NewDashboardManager(tmpDir, &DashboardConfig{Enabled: true}, t.Logf)
	if !mgr.IsEnabled() {
		t.Error("expected enabled with Enabled=true")
	}
}

func TestDashboardManager_PidFile(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewDashboardManager(tmpDir, DefaultDashboardConfig(), t.Logf)
	expected := filepath.Join(tmpDir, "daemon", "dashboard.pid")
	if got := mgr.pidFile(); got != expected {
		t.Errorf("pidFile() = %s, want %s", got, expected)
	}
}

func TestDashboardManager_Status_NotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := DefaultDashboardConfig()
	mgr := NewDashboardManager(tmpDir, cfg, t.Logf)

	status := mgr.Status()
	if status.Running {
		t.Error("expected not running")
	}
	if status.Port != DefaultDashboardPort {
		t.Errorf("expected port %d, got %d", DefaultDashboardPort, status.Port)
	}
	if status.URL != "" {
		t.Errorf("expected empty URL when not running, got %s", status.URL)
	}
}

func TestDashboardManager_EnsureRunning_Disabled(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := DefaultDashboardConfig() // Disabled by default
	mgr := NewDashboardManager(tmpDir, cfg, t.Logf)

	if err := mgr.EnsureRunning(); err != nil {
		t.Errorf("EnsureRunning on disabled manager should return nil, got %v", err)
	}
}

func TestDashboardManager_HealthCheckInterval(t *testing.T) {
	tmpDir := t.TempDir()

	// Default interval
	mgr := NewDashboardManager(tmpDir, DefaultDashboardConfig(), t.Logf)
	if got := mgr.HealthCheckInterval(); got != DefaultDashboardHealthCheckInterval {
		t.Errorf("expected default interval %v, got %v", DefaultDashboardHealthCheckInterval, got)
	}

	// Custom interval
	cfg := &DashboardConfig{HealthCheckInterval: 30 * time.Second}
	mgr = NewDashboardManager(tmpDir, cfg, t.Logf)
	if got := mgr.HealthCheckInterval(); got != 30*time.Second {
		t.Errorf("expected 30s interval, got %v", got)
	}
}

func TestDashboardIsRunning_NoPidFile(t *testing.T) {
	tmpDir := t.TempDir()
	running, pid := DashboardIsRunning(tmpDir)
	if running {
		t.Error("expected not running with no PID file")
	}
	if pid != 0 {
		t.Errorf("expected PID 0, got %d", pid)
	}
}

func TestDashboardIsRunning_StalePidFile(t *testing.T) {
	tmpDir := t.TempDir()
	daemonDir := filepath.Join(tmpDir, "daemon")
	os.MkdirAll(daemonDir, 0755)

	// Write a PID file with a dead PID (99999999 is almost certainly not running)
	pidFile := filepath.Join(daemonDir, "dashboard.pid")
	os.WriteFile(pidFile, []byte("99999999\nfakenonce"), 0644)

	running, _ := DashboardIsRunning(tmpDir)
	if running {
		t.Error("expected not running with stale PID")
	}
}

func TestIsPatrolEnabled_Dashboard(t *testing.T) {
	// nil config => disabled (opt-in)
	if IsPatrolEnabled(nil, "dashboard") {
		t.Error("expected dashboard disabled with nil config")
	}

	// Explicit enabled
	cfg := &DaemonPatrolConfig{
		Patrols: &PatrolsConfig{
			Dashboard: &DashboardConfig{Enabled: true, Port: 9090},
		},
	}
	if !IsPatrolEnabled(cfg, "dashboard") {
		t.Error("expected dashboard enabled")
	}

	// Explicit disabled
	cfg.Patrols.Dashboard.Enabled = false
	if IsPatrolEnabled(cfg, "dashboard") {
		t.Error("expected dashboard disabled")
	}
}
