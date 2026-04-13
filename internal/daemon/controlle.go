package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// DefaultControlleHealthCheckInterval is how often the dedicated Controlle health
// check ticker fires, independent of the general daemon heartbeat (3 min).
const DefaultControlleHealthCheckInterval = 120 * time.Second

// ControlleConfig holds configuration for the Controlle Telegram gateway.
type ControlleConfig struct {
	// Enabled controls whether the daemon manages the Controlle bot.
	Enabled bool `json:"enabled"`

	// DeployDir is the directory containing the Controlle source (src/index.ts).
	// Default: <townRoot>/../controlle/refinery/rig
	DeployDir string `json:"deploy_dir,omitempty"`

	// RuntimeDir is where PID and log files are stored.
	// Default: <townRoot>/../controlle/.runtime
	RuntimeDir string `json:"runtime_dir,omitempty"`

	// LogFile is the path to the Controlle bot log file.
	// Default: <RuntimeDir>/controlle.log
	LogFile string `json:"log_file,omitempty"`

	// AutoRestart controls whether to restart on crash.
	AutoRestart bool `json:"auto_restart,omitempty"`

	// RestartDelay is the initial delay before restarting after crash (default 5s).
	RestartDelay time.Duration `json:"restart_delay,omitempty"`

	// MaxRestartDelay is the maximum backoff delay (default 5min).
	MaxRestartDelay time.Duration `json:"max_restart_delay,omitempty"`

	// MaxRestartsInWindow is the maximum number of restarts allowed within
	// RestartWindow before escalating instead of retrying (default 5).
	MaxRestartsInWindow int `json:"max_restarts_in_window,omitempty"`

	// RestartWindow is the time window for counting restarts (default 10min).
	RestartWindow time.Duration `json:"restart_window,omitempty"`

	// HealthyResetInterval is how long the bot must stay healthy before
	// the backoff counter resets (default 5min).
	HealthyResetInterval time.Duration `json:"healthy_reset_interval,omitempty"`

	// HealthCheckInterval is how often to run the Controlle health check,
	// independent of the general daemon heartbeat. Default 120s.
	HealthCheckInterval time.Duration `json:"health_check_interval,omitempty"`

	// WorkDir is DEPRECATED — use DeployDir instead.
	// Kept for backward compatibility with existing daemon.json files.
	WorkDir string `json:"work_dir,omitempty"`
}

// ControlleServerManager manages the Controlle Telegram bot lifecycle.
// It follows the DoltServerManager pattern with exponential backoff,
// crash alerts, and escalation to the mayor.
type ControlleServerManager struct {
	config   *ControlleConfig
	townRoot string
	logger   func(format string, v ...interface{})

	mu        sync.Mutex
	process   *os.Process
	startedAt time.Time

	// Backoff state for restart logic
	currentDelay    time.Duration // Current backoff delay (grows exponentially)
	restartTimes    []time.Time   // Timestamps of recent restarts within window
	lastHealthyTime time.Time     // Last time the bot was confirmed healthy
	escalated       bool          // Whether we've already escalated (avoid spamming)
	restarting      bool          // Whether a restart is in progress

	// Test hooks (nil = use real implementations; set only in tests)
	startFn    func() error
	runningFn  func() (int, bool)
	stopFn     func()
	sleepFn    func(time.Duration)
	nowFn      func() time.Time
	escalateFn func(int)
	crashAlertFn func(int)
}

// NewControlleServerManager creates a new Controlle server manager.
func NewControlleServerManager(townRoot string, config *ControlleConfig, logger func(format string, v ...interface{})) *ControlleServerManager {
	if config == nil {
		config = &ControlleConfig{}
	}

	// Migrate deprecated WorkDir to DeployDir
	if config.DeployDir == "" && config.WorkDir != "" {
		config.DeployDir = config.WorkDir
	}

	// Apply defaults
	if config.DeployDir == "" {
		config.DeployDir = filepath.Join(townRoot, "..", "controlle", "refinery", "rig")
	}
	if config.RuntimeDir == "" {
		config.RuntimeDir = filepath.Join(townRoot, "..", "controlle", ".runtime")
	}
	if config.LogFile == "" {
		config.LogFile = filepath.Join(config.RuntimeDir, "controlle.log")
	}
	if config.RestartDelay <= 0 {
		config.RestartDelay = 5 * time.Second
	}
	if config.MaxRestartDelay <= 0 {
		config.MaxRestartDelay = 5 * time.Minute
	}
	if config.MaxRestartsInWindow <= 0 {
		config.MaxRestartsInWindow = 5
	}
	if config.RestartWindow <= 0 {
		config.RestartWindow = 10 * time.Minute
	}
	if config.HealthyResetInterval <= 0 {
		config.HealthyResetInterval = 5 * time.Minute
	}
	if config.HealthCheckInterval <= 0 {
		config.HealthCheckInterval = DefaultControlleHealthCheckInterval
	}

	return &ControlleServerManager{
		config:   config,
		townRoot: townRoot,
		logger:   logger,
	}
}

// NewControlleManager is a backward-compatible alias for NewControlleServerManager.
func NewControlleManager(townRoot string, config *ControlleConfig, logger func(format string, v ...interface{})) *ControlleManager {
	return NewControlleServerManager(townRoot, config, logger)
}

// ControlleManager is an alias for ControlleServerManager for backward compatibility.
type ControlleManager = ControlleServerManager

// IsEnabled returns whether Controlle management is enabled.
func (m *ControlleServerManager) IsEnabled() bool {
	return m.config != nil && m.config.Enabled
}

// HealthCheckInterval returns the configured health check interval.
func (m *ControlleServerManager) HealthCheckInterval() time.Duration {
	if m.config.HealthCheckInterval > 0 {
		return m.config.HealthCheckInterval
	}
	return DefaultControlleHealthCheckInterval
}

// Status returns a human-readable status string and process details.
func (m *ControlleServerManager) Status() (running bool, pid int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pid, running = m.isRunning()
	return
}

// StartedAt returns when the managed process was started.
func (m *ControlleServerManager) StartedAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startedAt
}

// DeployDir returns the configured deploy directory.
func (m *ControlleServerManager) DeployDir() string {
	return m.config.DeployDir
}

// LogFilePath returns the configured log file path.
func (m *ControlleServerManager) LogFilePath() string {
	return m.config.LogFile
}

func (m *ControlleServerManager) pidFile() string {
	return filepath.Join(m.config.RuntimeDir, "controlle.pid")
}

func (m *ControlleServerManager) now() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

func (m *ControlleServerManager) doSleep(d time.Duration) {
	if m.sleepFn != nil {
		m.sleepFn(d)
		return
	}
	time.Sleep(d)
}

// EnsureRunning checks if Controlle is running and starts it if not.
// Uses exponential backoff and a max-restart cap to avoid crash-looping.
func (m *ControlleServerManager) EnsureRunning() error {
	if !m.IsEnabled() {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Another goroutine is already restarting — skip to avoid double-starts
	if m.restarting {
		m.logger("Controlle restart already in progress, skipping")
		return nil
	}

	pid, running := m.isRunning()
	if running {
		// Bot is healthy (process alive) — reset backoff if stable long enough
		m.maybeResetBackoff()
		return nil
	}

	// Not running, start it
	if pid > 0 {
		m.logger("Controlle bot PID %d is dead, cleaning up and restarting...", pid)
		m.sendCrashAlert(pid)
	}
	return m.restartWithBackoff()
}

// isRunning checks if the Controlle process is alive.
// Must be called with m.mu held.
func (m *ControlleServerManager) isRunning() (int, bool) {
	if m.runningFn != nil {
		return m.runningFn()
	}

	// Check our tracked process first
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
		return pid, false
	}

	// Track the process
	process, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	m.process = process
	return pid, true
}

// Start launches the Controlle bot process (public API).
func (m *ControlleServerManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked()
}

// startLocked launches the Controlle bot process.
// Must be called with m.mu held.
func (m *ControlleServerManager) startLocked() error {
	if m.startFn != nil {
		return m.startFn()
	}

	// Ensure runtime directory exists
	if err := os.MkdirAll(m.config.RuntimeDir, 0755); err != nil {
		return fmt.Errorf("creating runtime dir: %w", err)
	}

	// Verify deploy dir exists with source
	srcPath := filepath.Join(m.config.DeployDir, "src", "index.ts")
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("controlle source not found at %s: %w", m.config.DeployDir, err)
	}

	// Open log file
	logFile, err := os.OpenFile(m.config.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}

	cmd := exec.Command("bun", "run", "src/index.ts")
	cmd.Dir = m.config.DeployDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Inherit environment (TELEGRAM_BOT_TOKEN must be set in daemon env)
	cmd.Env = os.Environ()
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("starting controlle: %w", err)
	}

	// Close log file handle — the child process has its own fd now
	logFile.Close()

	m.process = cmd.Process
	m.startedAt = m.now()

	// Write PID file
	if _, err := writePIDFile(m.pidFile(), cmd.Process.Pid); err != nil {
		m.logger("Warning: failed to write PID file: %v", err)
	}

	m.logger("Controlle bot started (pid %d)", cmd.Process.Pid)
	return nil
}

// Stop gracefully stops the Controlle bot.
func (m *ControlleServerManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	return nil
}

// stopLocked stops the bot process. Must be called with m.mu held.
func (m *ControlleServerManager) stopLocked() {
	if m.stopFn != nil {
		m.stopFn()
		return
	}

	if m.process == nil {
		// Check PID file for externally-started processes
		pid, alive, _ := verifyPIDOwnership(m.pidFile())
		if pid > 0 && alive {
			process, err := os.FindProcess(pid)
			if err == nil {
				m.process = process
			}
		}
	}

	if m.process == nil {
		return
	}

	m.logger("Stopping Controlle bot (pid %d)", m.process.Pid)

	// Send SIGTERM for graceful shutdown
	if err := sendTermSignal(m.process); err != nil {
		m.logger("Warning: failed to send SIGTERM to Controlle: %v", err)
	}

	// Wait up to 5 seconds for graceful shutdown
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessAlive(m.process) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Force kill if still alive
	if isProcessAlive(m.process) {
		m.logger("Controlle bot did not stop gracefully, sending SIGKILL")
		if err := sendKillSignal(m.process); err != nil {
			m.logger("Warning: failed to SIGKILL Controlle: %v", err)
		}
	}

	m.process = nil
	_ = os.Remove(m.pidFile())
}

// restartWithBackoff attempts to restart the bot with exponential backoff
// and a max-restart cap. If the cap is exceeded, it escalates instead of retrying.
// Must be called with m.mu held.
func (m *ControlleServerManager) restartWithBackoff() error {
	if !m.config.AutoRestart {
		return m.startLocked()
	}

	now := m.now()

	// Prune restart times outside the window
	m.pruneRestartTimes(now)

	// Check if we've exceeded the restart cap
	if len(m.restartTimes) >= m.config.MaxRestartsInWindow {
		if !m.escalated {
			m.escalated = true
			m.logger("Controlle restart cap reached (%d restarts in %v), escalating to mayor",
				len(m.restartTimes), m.config.RestartWindow)
			m.sendEscalationMail(len(m.restartTimes))
		}
		return fmt.Errorf("controlle restart cap exceeded (%d restarts in %v); escalated to mayor",
			len(m.restartTimes), m.config.RestartWindow)
	}

	// Mark restart in progress to prevent concurrent restarts during backoff sleep
	m.restarting = true
	defer func() { m.restarting = false }()

	// Apply exponential backoff delay
	delay := m.getBackoffDelay()
	if delay > 0 && len(m.restartTimes) > 0 {
		m.logger("Backing off %v before Controlle restart (attempt %d in window)",
			delay, len(m.restartTimes)+1)
		// Unlock during sleep so we don't hold the mutex during backoff
		m.mu.Unlock()
		m.doSleep(delay)
		m.mu.Lock()

		// Re-check after re-acquiring the lock: another goroutine may have
		// started the bot while we were sleeping (TOCTOU guard).
		if _, running := m.isRunning(); running {
			m.logger("Controlle started by another goroutine during backoff, skipping")
			return nil
		}
	}

	// Record this restart attempt
	m.restartTimes = append(m.restartTimes, m.now())

	// Advance the backoff for next time
	m.advanceBackoff()

	return m.startLocked()
}

// getBackoffDelay returns the current backoff delay.
func (m *ControlleServerManager) getBackoffDelay() time.Duration {
	if m.currentDelay <= 0 {
		return m.config.RestartDelay
	}
	return m.currentDelay
}

// advanceBackoff doubles the current delay up to MaxRestartDelay.
func (m *ControlleServerManager) advanceBackoff() {
	if m.currentDelay <= 0 {
		m.currentDelay = m.config.RestartDelay
	}
	m.currentDelay *= 2
	if m.currentDelay > m.config.MaxRestartDelay {
		m.currentDelay = m.config.MaxRestartDelay
	}
}

// pruneRestartTimes removes restart timestamps outside the configured window.
func (m *ControlleServerManager) pruneRestartTimes(now time.Time) {
	cutoff := now.Add(-m.config.RestartWindow)
	pruned := m.restartTimes[:0]
	for _, t := range m.restartTimes {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	m.restartTimes = pruned
}

// maybeResetBackoff resets backoff state if the bot has been healthy
// for the configured HealthyResetInterval.
// Must be called with m.mu held.
func (m *ControlleServerManager) maybeResetBackoff() {
	now := m.now()

	if m.lastHealthyTime.IsZero() {
		m.lastHealthyTime = now
		return
	}

	if now.Sub(m.lastHealthyTime) >= m.config.HealthyResetInterval {
		if m.currentDelay > 0 || len(m.restartTimes) > 0 || m.escalated {
			m.logger("Controlle bot healthy for %v, resetting backoff state", m.config.HealthyResetInterval)
			m.currentDelay = 0
			m.restartTimes = nil
			m.escalated = false
		}
		m.lastHealthyTime = now
	}
}

// sendEscalationMail sends a mail to the mayor when the Controlle bot has
// exceeded its restart cap, indicating a systemic issue.
// Runs the mail command asynchronously to avoid blocking the mutex.
func (m *ControlleServerManager) sendEscalationMail(restartCount int) {
	if m.escalateFn != nil {
		m.escalateFn(restartCount)
		return
	}
	subject := fmt.Sprintf("ESCALATION: Controlle bot crash-looping (%d restarts)", restartCount)
	body := fmt.Sprintf(`The Controlle Telegram gateway has restarted %d times within %v and has been capped.

The daemon will NOT restart it again until the backoff window expires or the issue is resolved.

Possible causes:
- TELEGRAM_BOT_TOKEN not set or expired
- Network connectivity issues
- Source code errors in %s
- Missing bun runtime

Deploy dir: %s
Log file: %s

Action needed: Investigate and fix the root cause, then restart with 'gt controlle restart'.`,
		restartCount, m.config.RestartWindow,
		m.config.DeployDir,
		m.config.DeployDir, m.config.LogFile)

	townRoot := m.townRoot
	logger := m.logger

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "gt", "mail", "send", "mayor/", "-s", subject, "-m", body) //nolint:gosec // G204: args are constructed internally
		setSysProcAttr(cmd)
		cmd.Dir = townRoot
		cmd.Env = os.Environ()

		if err := cmd.Run(); err != nil {
			logger("Warning: failed to send escalation mail to mayor: %v", err)
		} else {
			logger("Sent escalation mail to mayor about Controlle crash-loop")
		}
	}()
}

// sendCrashAlert sends a mail to the mayor when the Controlle bot is found dead.
// This is for single crash detection — distinct from crash-loop escalation.
// Runs asynchronously to avoid blocking.
func (m *ControlleServerManager) sendCrashAlert(deadPID int) {
	if m.crashAlertFn != nil {
		m.crashAlertFn(deadPID)
		return
	}
	subject := "ALERT: Controlle bot crashed"
	body := fmt.Sprintf(`The Controlle Telegram gateway (PID %d) was found dead. The daemon is restarting it.

Deploy dir: %s
Log file: %s

Check the log file for crash details. If crashes recur, the daemon will escalate after %d restarts in %v.`,
		deadPID,
		m.config.DeployDir, m.config.LogFile,
		m.config.MaxRestartsInWindow, m.config.RestartWindow)

	townRoot := m.townRoot
	logger := m.logger

	go func() {
		sendControlleAlertMail(townRoot, "mayor/", subject, body, logger)
	}()
}

// sendControlleAlertMail sends a Controlle alert mail to a specific recipient.
func sendControlleAlertMail(townRoot, recipient, subject, body string, logger func(format string, v ...interface{})) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gt", "mail", "send", recipient, "-s", subject, "-m", body) //nolint:gosec // G204: args are constructed internally
	setSysProcAttr(cmd)
	cmd.Dir = townRoot
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		logger("Warning: failed to send Controlle alert to %s: %v", recipient, err)
	}
}
