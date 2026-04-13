package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/daemon"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var controlleCmd = &cobra.Command{
	Use:     "controlle",
	GroupID: GroupServices,
	Short:   "Manage the Controlle Telegram gateway",
	RunE:    requireSubcommand,
	Long: `Manage the Controlle Telegram gateway bot for Gas Town.

The Controlle bot bridges Telegram with Gas Town agents, routing messages
between Telegram forum topics and agent sessions.

The bot is managed by the daemon with automatic restart on failure,
exponential backoff, and escalation to the mayor on repeated crashes.`,
}

var controlleStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Controlle bot",
	Long: `Start the Controlle Telegram gateway bot in the background.

The bot will run until stopped with 'gt controlle stop'.
Requires TELEGRAM_BOT_TOKEN to be set in the environment.`,
	RunE: runControlleStart,
}

var controlleStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the Controlle bot",
	Long:  `Stop the running Controlle Telegram gateway bot.`,
	RunE:  runControlleStop,
}

var controlleRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the Controlle bot",
	Long:  `Stop and restart the Controlle Telegram gateway bot.`,
	RunE:  runControlleRestart,
}

var controlleStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Controlle bot status",
	Long:  `Show the current status of the Controlle Telegram gateway bot.`,
	RunE:  runControlleStatus,
}

var controlleLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View Controlle bot logs",
	Long:  `View the Controlle bot log file.`,
	RunE:  runControlleLogs,
}

var (
	controlleLogLines  int
	controlleLogFollow bool
)

func init() {
	controlleCmd.AddCommand(controlleStartCmd)
	controlleCmd.AddCommand(controlleStopCmd)
	controlleCmd.AddCommand(controlleRestartCmd)
	controlleCmd.AddCommand(controlleStatusCmd)
	controlleCmd.AddCommand(controlleLogsCmd)

	controlleLogsCmd.Flags().IntVarP(&controlleLogLines, "lines", "n", 50, "Number of lines to show")
	controlleLogsCmd.Flags().BoolVarP(&controlleLogFollow, "follow", "f", false, "Follow log output")

	rootCmd.AddCommand(controlleCmd)
}

// loadControlleManager creates a ControlleServerManager from daemon.json config.
func loadControlleManager(townRoot string) *daemon.ControlleServerManager {
	patrolConfig := daemon.LoadPatrolConfig(townRoot)
	var config *daemon.ControlleConfig
	if patrolConfig != nil && patrolConfig.Patrols != nil && patrolConfig.Patrols.Controlle != nil {
		config = patrolConfig.Patrols.Controlle
	}
	if config == nil {
		config = &daemon.ControlleConfig{Enabled: true}
	}
	// Force enabled for manual CLI commands
	config.Enabled = true
	return daemon.NewControlleServerManager(townRoot, config, func(format string, v ...interface{}) {
		fmt.Printf(format+"\n", v...)
	})
}

func runControlleStart(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	mgr := loadControlleManager(townRoot)

	// Check if already running
	running, pid := mgr.Status()
	if running {
		fmt.Printf("%s Controlle bot already running (PID %d)\n", style.Dim.Render("○"), pid)
		return nil
	}

	// Verify source exists
	srcPath := filepath.Join(mgr.DeployDir(), "src", "index.ts")
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("controlle source not found at %s", mgr.DeployDir())
	}

	if err := mgr.Start(); err != nil {
		return err
	}

	_, newPID := mgr.Status()
	fmt.Printf("%s Controlle bot started (PID %d)\n", style.Bold.Render("✓"), newPID)
	fmt.Printf("  Deploy dir: %s\n", mgr.DeployDir())
	fmt.Printf("  Log file:   %s\n", mgr.LogFilePath())
	return nil
}

func runControlleStop(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	mgr := loadControlleManager(townRoot)

	running, pid := mgr.Status()
	if !running {
		fmt.Printf("%s Controlle bot is not running\n", style.Dim.Render("○"))
		return nil
	}

	if err := mgr.Stop(); err != nil {
		return err
	}

	fmt.Printf("%s Controlle bot stopped (was PID %d)\n", style.Bold.Render("✓"), pid)
	return nil
}

func runControlleRestart(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	mgr := loadControlleManager(townRoot)

	// Stop if running
	running, pid := mgr.Status()
	if running {
		fmt.Printf("Stopping Controlle bot (PID %d)...\n", pid)
		if err := mgr.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stop failed: %v\n", err)
		} else {
			fmt.Printf("%s Stopped\n", style.Bold.Render("✓"))
		}
		// Brief pause
		time.Sleep(500 * time.Millisecond)
	}

	// Start
	fmt.Println("Starting Controlle bot...")
	if err := mgr.Start(); err != nil {
		return fmt.Errorf("restart failed: %w", err)
	}

	_, newPID := mgr.Status()
	fmt.Printf("%s Controlle bot restarted (PID %d)\n", style.Bold.Render("✓"), newPID)
	fmt.Printf("  Deploy dir: %s\n", mgr.DeployDir())
	fmt.Printf("  Log file:   %s\n", mgr.LogFilePath())
	return nil
}

func runControlleStatus(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	mgr := loadControlleManager(townRoot)
	running, pid := mgr.Status()

	if running {
		startedAt := mgr.StartedAt()
		uptime := ""
		if !startedAt.IsZero() {
			uptime = fmt.Sprintf(" (uptime: %s)", time.Since(startedAt).Round(time.Second))
		}
		fmt.Printf("%s Controlle bot is %s (PID %d)%s\n",
			style.Bold.Render("●"),
			style.Bold.Render("running"),
			pid, uptime)
	} else {
		fmt.Printf("%s Controlle bot is %s\n",
			style.Dim.Render("○"),
			"not running")
	}

	fmt.Printf("  Deploy dir: %s\n", mgr.DeployDir())
	fmt.Printf("  Log file:   %s\n", mgr.LogFilePath())

	// Show config status
	patrolConfig := daemon.LoadPatrolConfig(townRoot)
	if patrolConfig != nil && patrolConfig.Patrols != nil && patrolConfig.Patrols.Controlle != nil {
		cfg := patrolConfig.Patrols.Controlle
		if cfg.Enabled {
			fmt.Printf("  Daemon:     %s\n", style.Bold.Render("managed (auto-restart enabled)"))
		} else {
			fmt.Printf("  Daemon:     %s\n", style.Dim.Render("disabled"))
		}
	} else {
		fmt.Printf("  Daemon:     %s\n", style.Dim.Render("not configured"))
	}

	return nil
}

func runControlleLogs(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	mgr := loadControlleManager(townRoot)
	logFile := mgr.LogFilePath()

	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		return fmt.Errorf("no log file found at %s", logFile)
	}

	if controlleLogFollow {
		tailCmd := exec.Command("tail", "-f", logFile)
		tailCmd.Stdout = os.Stdout
		tailCmd.Stderr = os.Stderr
		return tailCmd.Run()
	}

	tailCmd := exec.Command("tail", "-n", strconv.Itoa(controlleLogLines), logFile)
	tailCmd.Stdout = os.Stdout
	tailCmd.Stderr = os.Stderr
	return tailCmd.Run()
}
