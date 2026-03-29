// Package tmux manages tmux windows for agent observability.
// It opens a named window per executive wake and per subagent session,
// and periodically closes windows older than the configured age.
package tmux

import (
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const session = "bud"

const budLogPath = "~/Library/Logs/bud.log"

// OpenExecWindow opens a tmux window showing the bud daemon log for an executive wake.
func OpenExecWindow(focusID string) {
	openWindow("exec", focusID, "tail -f "+budLogPath)
}

// OpenSubagentWindow opens a tmux window showing bud log lines for the given subagent session.
func OpenSubagentWindow(sessionID string) {
	shortID := sessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	cmd := fmt.Sprintf("tail -f %s | grep --line-buffered 'subagent-%s'", budLogPath, shortID)
	openWindow("sub", sessionID, cmd)
}

func openWindow(windowType, id, command string) {
	if err := ensureSession(); err != nil {
		log.Printf("[tmux] cannot ensure session %q: %v", session, err)
		return
	}
	epoch := time.Now().Unix()
	shortID := id
	if len(shortID) > 6 {
		shortID = shortID[:6]
	}
	windowName := fmt.Sprintf("bud-%s-%d-%s", windowType, epoch, shortID)
	if err := exec.Command("tmux", "new-window", "-t", session+":", "-n", windowName, command).Run(); err != nil {
		log.Printf("[tmux] failed to open window %q: %v", windowName, err)
	}
}

func ensureSession() error {
	if exec.Command("tmux", "has-session", "-t", session).Run() == nil {
		return nil
	}
	return exec.Command("tmux", "new-session", "-d", "-s", session).Run()
}

// CloseOldWindows removes windows from the bud tmux session created more than
// maxAge ago. Silently returns 0 if tmux is not running or the session doesn't exist.
func CloseOldWindows(maxAge time.Duration) int {
	out, err := exec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_index}:#{window_name}").Output()
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge).Unix()
	var toKill []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		idx, name, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		// Window name format: bud-{type}-{epoch}-{id6}
		segs := strings.Split(name, "-")
		if len(segs) < 4 || segs[0] != "bud" {
			continue
		}
		epoch, err := strconv.ParseInt(segs[2], 10, 64)
		if err != nil {
			continue
		}
		if epoch < cutoff {
			toKill = append(toKill, idx)
		}
	}
	// Kill in reverse index order to avoid index shifting mid-loop.
	for i := len(toKill) - 1; i >= 0; i-- {
		if err := exec.Command("tmux", "kill-window", "-t", session+":"+toKill[i]).Run(); err != nil {
			log.Printf("[tmux] failed to kill window %s: %v", toKill[i], err)
		}
	}
	if n := len(toKill); n > 0 {
		log.Printf("[tmux] closed %d old window(s)", n)
	}
	return len(toKill)
}

// StartCleanupLoop runs CloseOldWindows on the given interval in a background goroutine.
func StartCleanupLoop(interval, maxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			CloseOldWindows(maxAge)
		}
	}()
}
