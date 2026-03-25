package daemon

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	// DefaultDashboardPort is the default HTTP port for the dashboard.
	DefaultDashboardPort = 8080

	// DefaultDashboardHealthCheckInterval is how often to check if the dashboard
	// is still responding. Faster than Dolt because the dashboard is a simple
	// HTTP server that should always respond quickly.
	DefaultDashboardHealthCheckInterval = 60 * time.Second
)

// DashboardConfig holds configuration for the dashboard service.
type DashboardConfig struct {
	// Enabled controls whether the daemon manages the dashboard.
	Enabled bool `json:"enabled"`

	// Port is the HTTP port (default 8080).
	Port int `json:"port,omitempty"`

	// Bind is the address to bind to (default 127.0.0.1).
	Bind string `json:"bind,omitempty"`

	// AutoRestart controls whether to restart on crash.
	AutoRestart bool `json:"auto_restart,omitempty"`

	// RestartDelay is the initial delay before restarting after crash (default 2s).
	RestartDelay time.Duration `json:"restart_delay,omitempty"`

	// MaxRestartDelay is the maximum backoff delay (default 2min).
	MaxRestartDelay time.Duration `json:"max_restart_delay,omitempty"`

	// MaxRestartsInWindow is the maximum number of restarts allowed within
	// RestartWindow before giving up (default 5).
	MaxRestartsInWindow int `json:"max_restarts_in_window,omitempty"`

	// RestartWindow is the time window for counting restarts (default 10min).
	RestartWindow time.Duration `json:"restart_window,omitempty"`

	// HealthCheckInterval is how often to check dashboard health.
	HealthCheckInterval time.Duration `json:"health_check_interval,omitempty"`
}

// DefaultDashboardConfig returns sensible defaults for dashboard config.
func DefaultDashboardConfig() *DashboardConfig {
	return &DashboardConfig{
		Enabled:             false, // Opt-in
		Port:                DefaultDashboardPort,
		Bind:                "127.0.0.1",
		AutoRestart:         true,
		RestartDelay:        2 * time.Second,
		MaxRestartDelay:     2 * time.Minute,
		MaxRestartsInWindow: 5,
		RestartWindow:       10 * time.Minute,
		HealthCheckInterval: DefaultDashboardHealthCheckInterval,
	}
}

// DashboardStatus represents the current status of the dashboard.
type DashboardStatus struct {
	Running   bool      `json:"running"`
	PID       int       `json:"pid,omitempty"`
	Port      int       `json:"port,omitempty"`
	Bind      string    `json:"bind,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	URL       string    `json:"url,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// DashboardManager manages the dashboard HTTP server lifecycle.
type DashboardManager struct {
	config   *DashboardConfig
	townRoot string
	logger   func(format string, v ...interface{})

	mu        sync.Mutex
	process   *os.Process
	startedAt time.Time

	// Backoff state for restart logic
	currentDelay time.Duration
	restartTimes []time.Time
	escalated    bool
	restarting   bool

	// Test hooks
	nowFn   func() time.Time
	sleepFn func(time.Duration)
}

// NewDashboardManager creates a new dashboard manager.
func NewDashboardManager(townRoot string, config *DashboardConfig, logger func(format string, v ...interface{})) *DashboardManager {
	if config == nil {
		config = DefaultDashboardConfig()
	}
	return &DashboardManager{
		config:   config,
		townRoot: townRoot,
		logger:   logger,
	}
}

// IsEnabled returns whether dashboard management is enabled.
func (m *DashboardManager) IsEnabled() bool {
	return m.config != nil && m.config.Enabled
}

// HealthCheckInterval returns the configured health check interval.
func (m *DashboardManager) HealthCheckInterval() time.Duration {
	if m.config != nil && m.config.HealthCheckInterval > 0 {
		return m.config.HealthCheckInterval
	}
	return DefaultDashboardHealthCheckInterval
}

func (m *DashboardManager) now() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

func (m *DashboardManager) doSleep(d time.Duration) {
	if m.sleepFn != nil {
		m.sleepFn(d)
		return
	}
	time.Sleep(d)
}

// pidFile returns the path to the dashboard PID file.
func (m *DashboardManager) pidFile() string {
	return filepath.Join(m.townRoot, "daemon", "dashboard.pid")
}

// logFile returns the path to the dashboard log file.
func (m *DashboardManager) logFile() string {
	return filepath.Join(m.townRoot, "daemon", "dashboard.log")
}

// Status returns the current status of the dashboard.
func (m *DashboardManager) Status() *DashboardStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	port := m.config.Port
	if port == 0 {
		port = DefaultDashboardPort
	}
	bind := m.config.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}

	status := &DashboardStatus{
		Port: port,
		Bind: bind,
	}

	pid, running := m.isRunning()
	status.Running = running
	status.PID = pid

	if running {
		status.StartedAt = m.startedAt
		displayHost := bind
		if displayHost == "0.0.0.0" {
			displayHost = "localhost"
		}
		status.URL = fmt.Sprintf("http://%s:%d", displayHost, port)
	}

	return status
}

// isRunning checks if the dashboard process is running.
// Must be called with m.mu held.
func (m *DashboardManager) isRunning() (int, bool) {
	// Check our tracked process
	if m.process != nil {
		if isProcessAlive(m.process) {
			return m.process.Pid, true
		}
		m.process = nil
	}

	// Check PID file
	pid, alive, err := verifyPIDOwnership(m.pidFile())
	if err != nil || pid == 0 {
		return 0, false
	}

	if !alive {
		_ = os.Remove(m.pidFile())
		return 0, false
	}

	// Verify it's actually listening on the expected port
	if !m.isListening() {
		_ = os.Remove(m.pidFile())
		return 0, false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	m.process = process
	return pid, true
}

// isListening checks if the dashboard port is accepting connections.
func (m *DashboardManager) isListening() bool {
	port := m.config.Port
	if port == 0 {
		port = DefaultDashboardPort
	}
	bind := m.config.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}
	addr := net.JoinHostPort(bind, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// EnsureRunning ensures the dashboard is running.
// If not running, starts it. Uses exponential backoff to avoid crash-looping.
func (m *DashboardManager) EnsureRunning() error {
	if !m.IsEnabled() {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Already restarting from another goroutine
	if m.restarting {
		return nil
	}

	_, running := m.isRunning()
	if running {
		return nil
	}

	// Not running — start it
	if !m.config.AutoRestart && m.startedAt.IsZero() {
		// First start is always allowed
	} else if !m.config.AutoRestart {
		m.logger("Dashboard is down but auto-restart is disabled")
		return nil
	}

	// Check restart rate limit
	now := m.now()
	window := m.config.RestartWindow
	if window == 0 {
		window = 10 * time.Minute
	}
	maxRestarts := m.config.MaxRestartsInWindow
	if maxRestarts == 0 {
		maxRestarts = 5
	}

	// Prune old restart times
	cutoff := now.Add(-window)
	recent := m.restartTimes[:0]
	for _, t := range m.restartTimes {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	m.restartTimes = recent

	if len(m.restartTimes) >= maxRestarts {
		if !m.escalated {
			m.escalated = true
			m.logger("Dashboard has restarted %d times in %v, giving up (escalating)", maxRestarts, window)
		}
		return fmt.Errorf("dashboard restart limit exceeded (%d in %v)", maxRestarts, window)
	}

	// Apply backoff delay
	if m.currentDelay > 0 {
		m.logger("Dashboard restart backoff: waiting %v", m.currentDelay)
		m.restarting = true
		m.mu.Unlock()
		m.doSleep(m.currentDelay)
		m.mu.Lock()
		m.restarting = false
	}

	m.logger("Starting dashboard on port %d", m.config.Port)
	if err := m.start(); err != nil {
		// Increase backoff
		if m.currentDelay == 0 {
			delay := m.config.RestartDelay
			if delay == 0 {
				delay = 2 * time.Second
			}
			m.currentDelay = delay
		} else {
			m.currentDelay *= 2
			maxDelay := m.config.MaxRestartDelay
			if maxDelay == 0 {
				maxDelay = 2 * time.Minute
			}
			if m.currentDelay > maxDelay {
				m.currentDelay = maxDelay
			}
		}
		return fmt.Errorf("starting dashboard: %w", err)
	}

	m.restartTimes = append(m.restartTimes, now)
	// Reset backoff on successful start
	m.currentDelay = 0
	m.escalated = false
	return nil
}

// start launches the dashboard process as a detached background process.
// Must be called with m.mu held.
func (m *DashboardManager) start() error {
	// Ensure daemon directory exists
	daemonDir := filepath.Join(m.townRoot, "daemon")
	if err := os.MkdirAll(daemonDir, 0755); err != nil {
		return fmt.Errorf("creating daemon directory: %w", err)
	}

	// Resolve gt binary path
	gtPath, err := exec.LookPath("gt")
	if err != nil {
		return fmt.Errorf("gt not found in PATH: %w", err)
	}

	port := m.config.Port
	if port == 0 {
		port = DefaultDashboardPort
	}
	bind := m.config.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}

	// Open log file for stdout/stderr
	logFile, err := os.OpenFile(m.logFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening dashboard log: %w", err)
	}

	cmd := exec.Command(gtPath, "dashboard", "--port", strconv.Itoa(port), "--bind", bind)
	cmd.Dir = m.townRoot
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from parent session
	}

	// Propagate essential env vars
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GT_TOWN_ROOT=%s", m.townRoot),
	)

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("starting dashboard process: %w", err)
	}

	// Close log file — the child process inherits the fd
	logFile.Close()

	m.process = cmd.Process
	m.startedAt = m.now()

	// Write PID file
	if _, err := writePIDFile(m.pidFile(), cmd.Process.Pid); err != nil {
		m.logger("Warning: failed to write dashboard PID file: %v", err)
	}

	// Release the process so it doesn't become a zombie
	go cmd.Wait()

	// Brief pause then verify it's listening
	m.mu.Unlock()
	time.Sleep(500 * time.Millisecond)
	m.mu.Lock()

	if !m.isListeningWithRetry(3, 500*time.Millisecond) {
		m.logger("Warning: dashboard started (PID %d) but not yet accepting connections on port %d", cmd.Process.Pid, port)
	} else {
		m.logger("Dashboard started (PID %d) on port %d", cmd.Process.Pid, port)
	}

	return nil
}

// isListeningWithRetry checks if the dashboard is listening, retrying a few times.
func (m *DashboardManager) isListeningWithRetry(retries int, delay time.Duration) bool {
	for i := 0; i < retries; i++ {
		if m.isListening() {
			return true
		}
		if i < retries-1 {
			m.mu.Unlock()
			time.Sleep(delay)
			m.mu.Lock()
		}
	}
	return false
}

// Stop gracefully stops the dashboard.
func (m *DashboardManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pid, running := m.isRunning()
	if !running {
		// Clean up PID file if it exists
		_ = os.Remove(m.pidFile())
		return nil
	}

	m.logger("Stopping dashboard (PID %d)", pid)

	// Send SIGTERM for graceful shutdown
	if m.process != nil {
		if err := m.process.Signal(syscall.SIGTERM); err != nil {
			m.logger("Warning: failed to send SIGTERM to dashboard: %v", err)
		}
	}

	// Wait up to 5 seconds for graceful shutdown
	deadline := m.now().Add(5 * time.Second)
	for m.now().Before(deadline) {
		m.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
		m.mu.Lock()
		if _, running := m.isRunning(); !running {
			m.logger("Dashboard stopped gracefully")
			_ = os.Remove(m.pidFile())
			m.process = nil
			return nil
		}
	}

	// Force kill
	if m.process != nil {
		m.logger("Dashboard did not stop gracefully, sending SIGKILL")
		_ = m.process.Kill()
	}

	_ = os.Remove(m.pidFile())
	m.process = nil
	return nil
}

// DashboardIsRunning checks if a dashboard process is running for the given town.
// This is a standalone function for use outside the daemon (e.g., gt status, gt up).
func DashboardIsRunning(townRoot string) (bool, int) {
	pidFile := filepath.Join(townRoot, "daemon", "dashboard.pid")
	pid, alive, err := verifyPIDOwnership(pidFile)
	if err != nil || !alive {
		return false, 0
	}
	return true, pid
}

// DashboardStart starts the dashboard as a detached process.
// Standalone function for use outside the daemon (e.g., gt up).
func DashboardStart(townRoot string, port int, bind string) error {
	config := DefaultDashboardConfig()
	config.Enabled = true
	if port > 0 {
		config.Port = port
	}
	if bind != "" {
		config.Bind = bind
	}
	mgr := NewDashboardManager(townRoot, config, func(format string, v ...interface{}) {
		fmt.Fprintf(os.Stderr, "dashboard: "+format+"\n", v...)
	})
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.start()
}

// DashboardStop stops a running dashboard process.
// Standalone function for use outside the daemon (e.g., gt down).
func DashboardStop(townRoot string) error {
	config := DefaultDashboardConfig()
	config.Enabled = true
	mgr := NewDashboardManager(townRoot, config, func(format string, v ...interface{}) {
		fmt.Fprintf(os.Stderr, "dashboard: "+format+"\n", v...)
	})
	return mgr.Stop()
}
