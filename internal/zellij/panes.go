// Package zellij manages zellij panes for agent observability.
// It opens a named pane per executive wake and per subagent session inside a
// "Bud Sessions" tab in the existing "bud" zellij session.
package zellij

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

// zellijBin returns the path to the zellij binary, checking common install
// locations that may not be in the launchd PATH.
func zellijBin() string {
	if path, err := exec.LookPath("zellij"); err == nil {
		return path
	}
	home := os.Getenv("HOME")
	candidates := []string{
		home + "/.cargo/bin/zellij",
		"/opt/homebrew/bin/zellij",
		"/usr/local/bin/zellij",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "zellij" // fall back; will fail with a clear error
}

const (
	zellijSession = "bud"
	tabName       = "Bud Sessions"
)

// execWindowOnce ensures EnsureExecWindow only opens one pane per process lifetime.
var execWindowOnce sync.Once

// EnsureExecWindow opens the persistent executive log pane exactly once per
// process lifetime. Subsequent calls are no-ops. logPath should point to the
// single persistent executive log (logs/exec/executive.log).
func EnsureExecWindow(logPath string) {
	execWindowOnce.Do(func() {
		openPane("bud-exec", "tail -n +1 -F "+logPath)
	})
}

// OpenExecWindow opens a zellij pane tailing the executive session event log.
// Deprecated: use EnsureExecWindow for the single persistent log pane.
func OpenExecWindow(focusID, logPath string) {
	shortID := focusID
	if len(shortID) > 6 {
		shortID = shortID[:6]
	}
	epoch := time.Now().Unix()
	paneName := fmt.Sprintf("exec-%d-%s", epoch, shortID)
	openPane(paneName, "tail -n +1 -F "+logPath)
}

const budLogPath = "~/Library/Logs/bud.log"

// OpenSubagentWindow opens a zellij pane tailing the subagent session log file.
// logPath is the per-session log file; uses tail -n +1 -F so the pane always
// shows from the beginning of the file, including the === PROMPT === header.
func OpenSubagentWindow(sessionID, logPath string) {
	shortID := sessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	epoch := time.Now().Unix()
	paneName := fmt.Sprintf("sub-%d-%s", epoch, shortID)
	openPane(paneName, "tail -n +1 -F "+logPath)
}

func openPane(paneName, command string) {
	if err := ensureTab(); err != nil {
		log.Printf("[zellij] cannot ensure tab %q: %v", tabName, err)
		return
	}
	// zellij run opens a new pane in the currently focused tab of the session.
	// Since ensureTab navigated to "Bud Sessions", the pane lands there.
	if err := exec.Command(zellijBin(),
		"--session", zellijSession,
		"run", "--name", paneName, "--",
		"sh", "-c", command,
	).Run(); err != nil {
		log.Printf("[zellij] failed to open pane %q: %v", paneName, err)
	}
}

// ensureTab navigates to "Bud Sessions" in the bud session, creating it if needed.
func ensureTab() error {
	return exec.Command(zellijBin(),
		"--session", zellijSession,
		"action", "go-to-tab-name", tabName, "--create",
	).Run()
}

// CloseOldPanes is a no-op placeholder. Zellij's CLI does not expose a
// list-panes-by-name command, so age-based cleanup is not yet implemented.
// Panes in "Bud Sessions" can be closed manually or via a future implementation.
func CloseOldPanes(_ time.Duration) int { return 0 }

// StartCleanupLoop is a no-op placeholder matching the tmux package API.
func StartCleanupLoop(_, _ time.Duration) {}
