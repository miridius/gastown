package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ControlleConfig holds configuration for the Controlle Telegram gateway.
type ControlleConfig struct {
	// Enabled controls whether the daemon manages the Controlle bot.
	Enabled bool `json:"enabled"`

	// WorkDir is the directory containing the Controlle source (index.ts).
	// Default: <townRoot>/../controlle/crew/sam
	WorkDir string `json:"work_dir,omitempty"`

	// RuntimeDir is where PID and log files are stored.
	// Default: <WorkDir>/.runtime
	RuntimeDir string `json:"runtime_dir,omitempty"`
}

// ControlleManager manages the Controlle Telegram bot lifecycle.
// It checks a PID file each heartbeat and starts the bot if not running.
type ControlleManager struct {
	config   *ControlleConfig
	townRoot string
	logger   func(format string, v ...interface{})

	mu        sync.Mutex
	process   *os.Process
	startedAt time.Time
}

// NewControlleManager creates a new Controlle manager.
func NewControlleManager(townRoot string, config *ControlleConfig, logger func(format string, v ...interface{})) *ControlleManager {
	if config == nil {
		config = &ControlleConfig{}
	}
	// Apply defaults
	if config.WorkDir == "" {
		config.WorkDir = filepath.Join(townRoot, "..", "controlle", "crew", "sam")
	}
	if config.RuntimeDir == "" {
		config.RuntimeDir = filepath.Join(config.WorkDir, ".runtime")
	}
	return &ControlleManager{
		config:   config,
		townRoot: townRoot,
		logger:   logger,
	}
}

// IsEnabled returns whether Controlle management is enabled.
func (m *ControlleManager) IsEnabled() bool {
	return m.config != nil && m.config.Enabled
}

func (m *ControlleManager) pidFile() string {
	return filepath.Join(m.config.RuntimeDir, "controlle.pid")
}

func (m *ControlleManager) logFile() string {
	return filepath.Join(m.config.RuntimeDir, "controlle.log")
}

// EnsureRunning checks if Controlle is running and starts it if not.
func (m *ControlleManager) EnsureRunning() error {
	if !m.IsEnabled() {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, running := m.isRunning()
	if running {
		return nil
	}

	return m.start()
}

// isRunning checks if the Controlle process is alive.
// Must be called with m.mu held.
func (m *ControlleManager) isRunning() (int, bool) {
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
		return 0, false
	}

	// Track the process
	process, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	m.process = process
	return pid, true
}

// start launches the Controlle bot process.
// Must be called with m.mu held.
func (m *ControlleManager) start() error {
	// Ensure runtime directory exists
	if err := os.MkdirAll(m.config.RuntimeDir, 0755); err != nil {
		return fmt.Errorf("creating runtime dir: %w", err)
	}

	// Verify workdir exists
	if _, err := os.Stat(filepath.Join(m.config.WorkDir, "src", "index.ts")); err != nil {
		return fmt.Errorf("controlle source not found at %s: %w", m.config.WorkDir, err)
	}

	// Open log file
	logFile, err := os.OpenFile(m.logFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}

	cmd := exec.Command("bun", "run", "--watch", "src/index.ts")
	cmd.Dir = m.config.WorkDir
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
	m.startedAt = time.Now()

	// Write PID file
	if _, err := writePIDFile(m.pidFile(), cmd.Process.Pid); err != nil {
		m.logger("Warning: failed to write PID file: %v", err)
	}

	m.logger("Controlle bot started (pid %d)", cmd.Process.Pid)
	return nil
}

// Stop gracefully stops the Controlle bot.
func (m *ControlleManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.process == nil {
		return
	}

	m.logger("Stopping Controlle bot (pid %d)", m.process.Pid)
	if err := sendTermSignal(m.process); err != nil {
		m.logger("Warning: failed to send SIGTERM to Controlle: %v", err)
	}

	m.process = nil
	_ = os.Remove(m.pidFile())
}
